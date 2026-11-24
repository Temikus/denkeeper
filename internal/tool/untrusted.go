package tool

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Temikus/denkeeper/internal/config"
)

// ValidateUntrustedPlugin rejects the plugin settings that run code outside a
// sandbox: type "subprocess" (a host process as Denkeeper's user) and a Docker
// network that shares the host's ("host", or "container:<id>", which joins
// another container's namespace and may be a host-network one). Config-MCP
// plugin_add always calls it, and REST POST /plugins does unless the key has
// admin. Operators keep both settings through TOML.
func ValidateUntrustedPlugin(cfg config.PluginConfig) error {
	if cfg.Type == "subprocess" {
		return fmt.Errorf("subprocess plugins run on the host; only an operator can add one (use type \"docker\")")
	}
	network := strings.ToLower(strings.TrimSpace(cfg.Network))
	if network == "host" || strings.HasPrefix(network, "container:") {
		return fmt.Errorf("network %q shares a network namespace outside the sandbox; only an operator can set it", cfg.Network)
	}
	if len(cfg.Volumes) > 0 {
		return fmt.Errorf("volumes mount host paths into the container; only an operator can set them")
	}
	return nil
}

// CommandAllowed reports whether command may be spawned by a tool added or
// updated at runtime: true when [mcp] allowed_commands is empty (no
// allowlist), otherwise only on an exact match. Matching is on the full
// string, never the base name, so "/tmp/x/npx" does not pass for "npx".
func (m *Manager) CommandAllowed(command string) bool {
	if len(m.mcpCfg.AllowedCommands) == 0 {
		return true
	}
	return slices.Contains(m.mcpCfg.AllowedCommands, command)
}

// HasCommandAllowlist reports whether [mcp] allowed_commands is set.
func (m *Manager) HasCommandAllowlist() bool {
	return len(m.mcpCfg.AllowedCommands) > 0
}

// checkRuntimeCommand is the runtime gate for a stdio tool's command, args and
// env, run by LifecycleManager.AddTool and UpdateTool on every surface that
// adds or edits tools (config MCP, REST). TOML-loaded tools do not pass
// through it: the file is the operator's.
func (m *Manager) checkRuntimeCommand(cfg config.ToolConfig) error {
	if cfg.Transport != "" && cfg.Transport != "stdio" {
		return nil
	}
	if !m.CommandAllowed(cfg.Command) {
		return fmt.Errorf("command %q is not in [mcp] allowed_commands", cfg.Command)
	}
	if err := ValidatePinnedPackageArgs(cfg.Command, cfg.Args); err != nil {
		return err
	}
	return ValidateRuntimeEnv(cfg.Env)
}

// runtimeEnvRefused names env variables a runtime-added stdio tool may not
// set: each makes the child load other code (an interpreter option, a module
// or library path, a shell startup file, PATH for a launcher's `env node`) or
// fetch its package from elsewhere (a home, prefix or Windows profile dir
// holding .npmrc, pip.conf or uv.toml, or a CA bundle that lets a proxy
// answer). cfg.Env wins over the inherited env (buildStdioEnv), so without
// this a pin or an allowlisted command could still run anything.
var runtimeEnvRefused = flagSet(
	"NODE_OPTIONS", "NODE_PATH", "NODE_EXTRA_CA_CERTS",
	"PYTHONPATH", "PYTHONSTARTUP", "PYTHONHOME", "PYTHONUSERBASE",
	"GCONV_PATH", "LOCPATH", "NLSPATH", // glibc loads iconv modules and locale data from these
	"BASH_ENV", "ENV",
	"JAVA_TOOL_OPTIONS", "_JAVA_OPTIONS", "JDK_JAVA_OPTIONS",
	"PERL5OPT", "PERL5LIB", "RUBYOPT", "RUBYLIB",
	"PATH", "HOME", "PREFIX", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
)

// runtimeEnvRefusedPrefixes cover launchers whose every setting is also an
// env variable (npm, pip, uv), the Linux and macOS loaders, and the XDG base
// dirs, where npm, pip and uv look for config and cache (uv also installs
// tools there). uv alone has dozens that move the index or the interpreter,
// so whole families are refused.
var runtimeEnvRefusedPrefixes = []string{"NPM_CONFIG_", "PIP_", "UV_", "LD_", "DYLD_", "XDG_"}

// runtimeRefusalFix ends every runtime refusal. The checks apply to admin
// callers too, so the fix is the config file, not a permission.
const runtimeRefusalFix = "put this tool in the config file instead"

// ValidateRuntimeEnv refuses env names that load code or move a package's
// source, matched case-insensitively (npm reads npm_config_* in any case).
// LifecycleManager runs it for every runtime stdio add and env change.
func ValidateRuntimeEnv(env map[string]string) error {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names) // the same input names the same variable every time
	for _, name := range names {
		upper := strings.ToUpper(name)
		refused := runtimeEnvRefused[upper]
		for _, p := range runtimeEnvRefusedPrefixes {
			refused = refused || strings.HasPrefix(upper, p)
		}
		if refused {
			return fmt.Errorf("env %s can make the tool run other code or fetch its package from elsewhere; tools added at runtime can't set it, so %s", name, runtimeRefusalFix)
		}
	}
	return nil
}

// ValidatePinnedPackageArgs rejects npx and uvx invocations whose package is
// not pinned to an exact version. Those launchers download and run whatever
// the registry serves at spawn time, so an unpinned package lets a later
// publish — or a typo-squat — change the code running on the host with no
// config change. Other commands pass untouched.
func ValidatePinnedPackageArgs(command string, args []string) error {
	switch launcherName(command) {
	case "npx":
		return npxLauncher.check(args)
	case "uvx":
		return uvxLauncher.check(args)
	}
	return nil
}

// launcherName is the command's base name without a Windows extension.
func launcherName(command string) string {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(command, `\`, "/")))
	for _, ext := range []string{".cmd", ".exe", ".bat", ".ps1"} {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// npmPinned matches a registry package at an exact version:
// name@1.2.3 or @scope/name@1.2.3, with an optional prerelease/build suffix.
// Tags (latest, next), ranges (^1, ~1.2, 1.x) and git/URL/path specs fail.
var npmPinned = regexp.MustCompile(`^(@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*@v?\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)

// pyPinned matches a package at an exact version, in either spelling uvx
// accepts: name==1.2.3 or name@1.2.3, with optional [extras].
var pyPinned = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9._,-]+\])?(==|@)v?\d+(\.\d+)*([A-Za-z0-9.+!-]*)$`)

// bareVersion matches an interpreter version such as 3, 3.12 or 3.12.4.
var bareVersion = regexp.MustCompile(`^\d+(\.\d+){0,2}$`)

// packageFlagRole says what a package-taking flag's value is.
type packageFlagRole int

const (
	// namesPackage: the value is the package to run, so the first
	// positional is a binary inside it rather than a package.
	namesPackage packageFlagRole = iota + 1
	// extraPackage: the value is an additional dependency.
	extraPackage
)

// launcher describes one package launcher's command line well enough to find
// every package it will download.
type launcher struct {
	name         string
	refused      map[string]bool            // flags that run unpinned or arbitrary code
	packageFlags map[string]packageFlagRole // flags whose value is a package spec
	versionFlags map[string]bool            // flags whose value must be a bare version
	valueFlags   map[string]bool            // other known flags that take a separate value
	boolFlags    map[string]bool            // known flags that take no value
	pinned       *regexp.Regexp
	hint         string // an example of a pinned spec, for the error
}

func flagSet(names ...string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

var npxLauncher = launcher{
	name: "npx",
	// A pin says nothing about where the package comes from: a registry or
	// npm config the caller chooses can serve anything under that version.
	refused: flagSet("-c", "--call", "--registry", "--userconfig", "--globalconfig",
		"--node-options", "--script-shell"),
	packageFlags: map[string]packageFlagRole{"-p": namesPackage, "--package": namesPackage},
	valueFlags:   flagSet("--loglevel"),
	boolFlags: flagSet("-y", "--yes", "--no", "-q", "--quiet", "--silent",
		"--prefer-offline", "--prefer-online", "--offline", "--no-update-notifier",
		"--no-fund", "--no-audit"),
	pinned: npmPinned,
	hint:   "name@1.2.3",
}

var uvxLauncher = launcher{
	name: "uvx",
	// Index and find-links overrides pick where a pinned name is fetched
	// from; --allow-insecure-host lets anyone on the path answer instead.
	// Overrides and constraints files replace the pinned version, a config
	// file, directory or project can load a uv.toml that moves the index, and
	// a cache dir the caller filled can serve a planted build of the pin.
	refused: flagSet("--with-requirements", "--with-editable",
		"--index", "--index-url", "-i", "--default-index", "--extra-index-url",
		"--find-links", "-f", "--allow-insecure-host", "--env-file",
		"--overrides", "--constraints", "-c", "--build-constraints", "-b",
		"--config-file", "--directory", "--project", "--cache-dir"),
	packageFlags: map[string]packageFlagRole{
		"--from": namesPackage, "--with": extraPackage, "--with-executables-from": extraPackage,
	},
	// A path here would run that executable as the interpreter.
	versionFlags: flagSet("--python", "-p"),
	valueFlags: flagSet(
		"--color",
		"--index-strategy", "--keyring-provider", "--resolution", "--prerelease",
		"--exclude-newer", "--link-mode", "--python-platform",
		"--refresh-package", "--reinstall-package", "--upgrade-package", "-P",
		"--no-build-package", "--no-binary-package", "--no-build-isolation-package",
	),
	boolFlags: flagSet("-q", "--quiet", "-v", "--verbose", "-n", "--no-cache", "--offline",
		"--isolated", "--refresh", "--reinstall", "--upgrade", "-U", "--native-tls",
		"--no-progress", "--no-config", "--no-python-downloads", "--managed-python",
		"--no-managed-python", "--no-build-isolation", "--no-build", "--no-binary",
		"--no-sources", "--compile-bytecode", "--no-index"),
	pinned: pyPinned,
	hint:   "name==1.2.3",
}

// check walks the launcher's options up to the first positional argument.
// An option it does not know is refused rather than guessed at: guessing that
// it takes no value would let "--unknown pkg==1.0 evil" pass on the pinned-
// looking value while the launcher runs evil.
func (l launcher) check(args []string) error {
	named := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch {
		case l.refused[name]:
			return fmt.Errorf("%s %s can change what code the tool runs, so a pin cannot cover it; tools added at runtime can't use it, so %s", l.name, name, runtimeRefusalFix)
		case l.packageFlags[name] != 0:
			if !hasValue {
				if i+1 >= len(args) {
					return fmt.Errorf("%s %s needs a package", l.name, name)
				}
				i++
				value = args[i]
			}
			if err := l.requirePinned(value); err != nil {
				return err
			}
			named = named || l.packageFlags[name] == namesPackage
		case l.versionFlags[name]:
			var err error
			if i, err = l.checkVersionFlag(args, i, name, value, hasValue); err != nil {
				return err
			}
		case l.valueFlags[name]:
			if !hasValue {
				i++ // skip the flag's value
			}
		case l.boolFlags[name]:
		case arg == "--":
			return l.checkPositional(named, args[i+1:])
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("%s option %s is not one Denkeeper can check; tools added at runtime can't use it, so %s", l.name, name, runtimeRefusalFix)
		default:
			return l.checkPositional(named, args[i:])
		}
	}
	return l.checkPositional(named, nil)
}

// checkVersionFlag checks a versionFlags value, given inline or as the next
// argument, and returns the index of the last argument it consumed.
func (l launcher) checkVersionFlag(args []string, i int, name, value string, hasValue bool) (int, error) {
	if !hasValue {
		i++
		value = ""
		if i < len(args) {
			value = args[i]
		}
	}
	if !bareVersion.MatchString(value) {
		return i, fmt.Errorf("%s %s takes only a version such as 3.12 in a tool added at runtime; to name an interpreter, %s", l.name, name, runtimeRefusalFix)
	}
	return i, nil
}

// checkPositional checks the first positional argument, which is the package
// unless a flag already named it (then it is a binary inside that package).
func (l launcher) checkPositional(named bool, rest []string) error {
	if named {
		return nil
	}
	if len(rest) == 0 {
		return fmt.Errorf("%s needs a package to run", l.name)
	}
	return l.requirePinned(rest[0])
}

func (l launcher) requirePinned(spec string) error {
	if !l.pinned.MatchString(spec) {
		return fmt.Errorf("%s package %q is not pinned to an exact version; use %s", l.name, spec, l.hint)
	}
	return nil
}

// CommandLineChanged reports whether an update changes what a stdio tool
// spawns: its transport, command or arguments.
func CommandLineChanged(oldCfg, cfg config.ToolConfig) bool {
	return transportOf(oldCfg) != transportOf(cfg) ||
		oldCfg.Command != cfg.Command ||
		!slices.Equal(oldCfg.Args, cfg.Args)
}

func transportOf(cfg config.ToolConfig) string {
	if cfg.Transport == "" {
		return "stdio"
	}
	return cfg.Transport
}
