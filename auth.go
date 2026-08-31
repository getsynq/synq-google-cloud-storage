package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"

	qualityoauth "github.com/getsynq/quality-oauth-go"
	"github.com/getsynq/synq-google-cloud-storage/config"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2/clientcredentials"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/oauth"
)

// toolName is the binary, the OAuth software id and the entity-group prefix all
// at once, so a credential and a client registration can be traced back to it.
const toolName = "synq-google-cloud-storage"

// declaredScopes is what a sync needs and nothing more. Sending them is what
// keeps the consent screen honest: a login that declares no scope receives the
// caller's whole role-derived envelope.
var declaredScopes = []string{
	"SCOPE_ENTITY_READ",
	"SCOPE_ENTITY_EDIT",
	"SCOPE_ENTITY_TYPE_EDIT",
	"SCOPE_LINEAGE_READ",
	"SCOPE_LINEAGE_EDIT",
}

// writeScopes are the ones a sync cannot publish anything without. They reach a
// personal token through ROLE_OWNER (shown as Admin in the UI) and above.
var writeScopes = []string{
	"SCOPE_ENTITY_EDIT",
	"SCOPE_ENTITY_TYPE_EDIT",
	"SCOPE_LINEAGE_EDIT",
}

// authApp identifies this integration to the authorization server and to the
// credential store it shares with the other Coalesce Quality tools.
//
// FirstPartyClientID is deliberately empty: the authorization server seeds a
// client row per released first-party CLI and this is not one of them. An id it
// does not know is rejected at the authorize endpoint, and the login then waits
// for a callback that never arrives — so the client is registered dynamically
// instead.
func authApp() qualityoauth.App {
	return qualityoauth.App{
		Name:       toolName,
		Version:    version,
		SoftwareID: toolName,
		Scopes:     declaredScopes,
		ClientURI:  "https://github.com/getsynq/" + toolName,
	}
}

// resolveTarget picks the deployment to talk to. The precedence lives in the
// library, so this tool answers --region the same way every other Coalesce
// Quality CLI does; the config file is offered as the configured endpoint, which
// loses to a flag and wins over the environment. Explicit configuration above
// the ambient environment is the library's ordering, not this tool's, and
// `TestTheConfigFileOutranksTheEnvironment` pins it.
func resolveTarget(cfg *config.Config) (qualityoauth.Target, error) {
	// A flag naming a deployment is the user overriding the file, which is the
	// one case where a bad quality.region must not be fatal: refusing there makes
	// the typo unfixable except by editing the file it is in.
	typed := cfg.Quality.RegionFlag != "" || cfg.Quality.EndpointFlag != ""

	configured := cfg.Quality.Endpoint
	if configured == "" && cfg.Quality.Region != "" {
		target, err := qualityoauth.TargetForRegion(cfg.Quality.Region)
		switch {
		case err == nil:
			configured = target.Endpoint
		case typed:
			slog.Default().Warn("Ignoring quality.region in the config file", slog.String("error", err.Error()))
		default:
			return qualityoauth.Target{}, fmt.Errorf("quality.region in the config file: %w", err)
		}
	}
	return qualityoauth.Sources{
		RegionFlag:         cfg.Quality.RegionFlag,
		EndpointFlag:       cfg.Quality.EndpointFlag,
		ConfiguredEndpoint: configured,
	}.Resolve()
}

const authenticationHelp = `Authenticate with one of:
  - Browser login:       ` + toolName + ` auth login
  - Client credentials:  QUALITY_CLIENT_ID + QUALITY_CLIENT_SECRET
  - Pre-issued token:    QUALITY_TOKEN`

// connect opens an authenticated connection to the public API.
//
// Credentials resolve the way every Coalesce Quality tool resolves them: client
// credentials from the environment first, then a pre-issued token, then the
// config file's own pair, then the browser login in the shared credential slot.
// The unattended paths deliberately win, so a scheduled sync never picks up a
// developer's browser session.
func connect(ctx context.Context, cfg *config.Config, target qualityoauth.Target) (*grpc.ClientConn, error) {
	logger := slog.Default()

	if envCred := qualityoauth.EnvCredentialFromEnv(); envCred.Found() {
		switch envCred.Kind {
		case qualityoauth.EnvCredentialClient:
			logger.DebugContext(ctx, "Authenticating with client credentials from the environment")
			return dialClientCredentials(ctx, cfg, target, envCred.ClientID, envCred.ClientSecret)
		case qualityoauth.EnvCredentialToken:
			logger.DebugContext(ctx, "Authenticating with a pre-issued token from the environment")
			return dial(target, grpc.WithPerRPCCredentials(staticToken(envCred.Token)))
		}
	}

	if cfg.Quality.ClientID != "" && cfg.Quality.ClientSecret != "" {
		logger.DebugContext(ctx, "Authenticating with client credentials from the configuration")
		return dialClientCredentials(ctx, cfg, target, cfg.Quality.ClientID, cfg.Quality.ClientSecret)
	}

	if cfg.Quality.Token != "" {
		logger.DebugContext(ctx, "Authenticating with a pre-issued token from the configuration")
		return dial(target, grpc.WithPerRPCCredentials(staticToken(cfg.Quality.Token)))
	}

	app := authApp()
	if _, err := app.MigrateLegacyCredentials(target, nil); err != nil {
		return nil, err
	}
	cred, err := app.Credential(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("no credentials available for %s: %w\n\n%s", target, err, authenticationHelp)
	}
	logger.DebugContext(ctx, "Authenticating with the stored browser-login credential",
		slog.String("profile", cred.Profile),
		slog.String("source", string(cred.Source)),
	)
	if len(cred.MissingScopes) > 0 {
		logger.WarnContext(ctx, "The stored credential is missing scopes this sync needs",
			slog.Any("missing", cred.MissingScopes),
		)
	}
	return dial(target, grpc.WithPerRPCCredentials(staticToken(cred.Token.AccessToken)))
}

func dialClientCredentials(
	ctx context.Context,
	cfg *config.Config,
	target qualityoauth.Target,
	clientID, clientSecret string,
) (*grpc.ClientConn, error) {
	tokenEndpoint, err := tokenURL(target, cfg.Quality.OAuthURL)
	if err != nil {
		return nil, err
	}
	oauthConfig := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenEndpoint,
	}
	return dial(target, grpc.WithPerRPCCredentials(oauth.TokenSource{TokenSource: oauthConfig.TokenSource(ctx)}))
}

// tokenURL is where the client-credentials grant posts the client id and secret.
// It is derived from the deployment; an override still wins, because a
// self-hosted deployment can serve the authorization server from somewhere other
// than the API host.
//
// The override must be https. It carries the credentials as form fields, and the
// pair it carries is whichever one the run resolved — including one exported into
// the environment by CI, which the config file naming the host never saw.
// Loopback is excepted: a local authorization server never puts the credentials
// on a network, and demanding a certificate for it only teaches people to turn
// the check off.
func tokenURL(target qualityoauth.Target, override string) (string, error) {
	if override == "" {
		return qualityoauth.OAuthTokenURL(target), nil
	}
	parsed, err := url.Parse(override)
	if err != nil {
		return "", fmt.Errorf("quality.oauth_url %q: %w", override, err)
	}
	if parsed.Scheme != "https" && !isLoopback(parsed.Hostname()) {
		return "", fmt.Errorf(
			"quality.oauth_url %q must use https: it carries the client id and secret, which this sync may have read from the environment",
			override,
		)
	}
	return override, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func dial(target qualityoauth.Target, authOpt grpc.DialOption) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(target.Endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
		authOpt,
		grpc.WithAuthority(target.Host),
	)
	if err != nil {
		return nil, fmt.Errorf("connecting to the Coalesce Quality API at %s: %w", target.Endpoint, err)
	}
	return conn, nil
}

// staticToken sends a bearer token the server exchanges itself.
type staticToken string

func (t staticToken) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(t)}, nil
}

func (t staticToken) RequireTransportSecurity() bool { return true }

// ============================================================================
// auth commands
// ============================================================================

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage Coalesce Quality authentication",
	Long: `Manage authentication for the Coalesce Quality API.

Credentials are cached under ~/.synq/oauth/, partitioned by deployment, and
shared with the other Coalesce Quality tools — so a login done by synqctl or
synq-recon already serves this integration, and the other way round.

Publishing entities needs SCOPE_ENTITY_EDIT, SCOPE_ENTITY_TYPE_EDIT and
SCOPE_LINEAGE_EDIT, which reach a personal token through ROLE_OWNER (Admin in
the UI) and above. An account without them can still run --dry-run.`,
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with Coalesce Quality in a browser",
	Long: `Authenticate using the OAuth2 authorization code flow with PKCE.

Opens a browser, then caches the credential in the shared slot so every
Coalesce Quality tool on this machine can use it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := targetFromCommand(cmd)
		if err != nil {
			return err
		}
		app := authApp()
		if result, err := app.MigrateLegacyCredentials(target, nil); err != nil {
			return err
		} else if result.Migrated() {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Adopted the existing credential for %s from the previous store layout.\n", target)
		}

		tok, err := app.Login(cmd.Context(), target)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nAuthenticated against %s.\n", target)
		if tok.Workspace != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s\n", tok.Workspace)
		}
		if missing := qualityoauth.MissingScopes(tok.Scopes, writeScopes); len(missing) > 0 {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"\nThis account cannot publish entities; %v were not granted.\n"+
					"Syncs will work with --dry-run. Publishing needs ROLE_OWNER (Admin) or above.\n", missing)
		}
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show which deployments have a stored credential",
	RunE: func(cmd *cobra.Command, args []string) error {
		statuses, err := authApp().Status(cmd.Context())
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(statuses) == 0 {
			_, _ = fmt.Fprintf(out, "No stored credentials. Run `%s auth login`.\n", toolName)
			return nil
		}
		for _, s := range statuses {
			where := s.Region
			if where == "" {
				where = s.Endpoint
			}
			flow := s.Flow
			if flow == "" {
				flow = "consent"
			}
			state := "valid"
			if !s.Valid {
				state = "expired"
				if s.Refreshable {
					state = "expired, refreshable"
				}
			}
			// A credential with no recorded scopes is not proof of anything: it
			// predates scopes being stored. Saying "can publish" there would be a
			// guess dressed up as a fact.
			writes := "scopes not recorded"
			switch {
			case len(s.Scopes) == 0:
			case s.CoversScopes(writeScopes):
				writes = "can publish"
			default:
				writes = "read-only"
			}
			_, _ = fmt.Fprintf(out, "%s\tflow=%s\tprofile=%s\tworkspace=%s\t%s\t%s\n",
				where, flow, s.Profile, s.Workspace, state, writes)
		}
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the stored credential for a deployment",
	RunE: func(cmd *cobra.Command, args []string) error {
		target, err := targetFromCommand(cmd)
		if err != nil {
			return err
		}
		removed, err := authApp().Logout(cmd.Context(), target)
		if err != nil {
			return err
		}
		if !removed {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No stored credential for %s.\n", target)
			return nil
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed the stored credential for %s.\n", target)
		return nil
	},
}

// targetFromCommand resolves the deployment for an auth subcommand. It reads the
// same configuration a sync does, so `auth login` and a sync can never disagree
// about which deployment they mean.
func targetFromCommand(cmd *cobra.Command) (qualityoauth.Target, error) {
	configPath, _ := cmd.Flags().GetString("config")
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		// A sync needs a GCP project; logging in does not, so the validation
		// error is a warning here. The parsed file comes back with it and is
		// still the best answer available.
		_, _ = fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	if cfg == nil {
		// The file could not be parsed at all, so there is nothing to resolve
		// from beyond the flags the library reads for itself.
		return qualityoauth.Sources{}.Resolve()
	}
	return resolveTarget(cfg)
}

func init() {
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
}
