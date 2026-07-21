package cli

import (
	"fmt"
	"io"
	"strings"
)

type command struct {
	name     string
	summary  string
	usage    string
	details  []string
	examples []string
	children []command
}

var rootCommand = command{
	name:    "ovpn",
	summary: "manage an OpenVPN schema 4 instance",
	usage:   "ovpn <command> [options]",
	details: []string{
		"Run 'ovpn help COMMAND' or 'ovpn COMMAND -h' for command help.",
		"Use -v for the short version, or -V/--version for the full version report.",
		"Client, state, and runtime groups default to list, doctor, and status respectively.",
	},
	children: []command{
		group("server", "initialize, run, or render the OpenVPN server",
			leaf("init", "initialize a schema 4 instance", "ovpn server init",
				[]string{"Requires a valid declarative YAML configuration and an empty data directory."},
				"ovpn server init"),
			leaf("run", "supervise OpenVPN and its broker", "ovpn server run",
				[]string{"Uses the last applied SQLite configuration; YAML drift is reported but not applied."},
				"ovpn server run"),
			leaf("render", "render the applied server configuration", "ovpn server render [--output|-o FILE|-]",
				[]string{"Writes to stdout by default. Use '-' to request stdout explicitly."},
				"ovpn server render", "ovpn server render -o server.conf")),
		group("config", "validate, inspect, plan, or apply declarative configuration",
			leaf("validate", "validate the desired YAML configuration", "ovpn config validate [--json|-j]",
				[]string{"Does not open SQLite or change applied state."},
				"ovpn config validate", "ovpn config validate -j"),
			leaf("show", "show the applied SQLite configuration", "ovpn config show [--json|-j]",
				[]string{"Reads the applied snapshot, not the desired YAML file."},
				"ovpn config show"),
			leaf("export", "export applied configuration as YAML", "ovpn config export [--output|-o FILE|-]",
				[]string{"Writes to stdout by default. File output is created with mode 0600 and is never overwritten."},
				"ovpn config export -o -", "ovpn config export -o config.yaml"),
			leaf("plan", "plan desired-to-applied configuration changes", "ovpn config plan [--json|-j]",
				[]string{"Read-only; reports restart, firewall, address, artifact, and profile impact."},
				"ovpn config plan"),
			leaf("apply", "apply desired configuration while OpenVPN is stopped", "ovpn config apply [--yes|-y] [--json|-j]",
				[]string{"Requires confirmation and an exclusive runtime lock. It never reloads OpenVPN online."},
				"ovpn config apply", "ovpn config apply -y -j")),
		group("client", "manage client identities, profiles, and addresses",
			leaf("create", "create a client and its credentials", "ovpn client create NAME [--ipv4|-4 [auto|dynamic|ADDRESS]]",
				[]string{"IPv4 defaults to auto, the lowest available static address. A bare -4 also means auto."},
				"ovpn client create laptop", "ovpn client create phone -4 dynamic"),
			leaf("list", "list active and revoked clients", "ovpn client list [--detail|-d] [--full-id|-u] [--json|-j]",
				[]string{"Text output uses short IDs by default; JSON always contains full UUIDs."},
				"ovpn client list", "ovpn client list -d"),
			leaf("export", "export an active client profile", "ovpn client export (NAME|--name|-n NAME|--id|-i ID) [--output|-o FILE|-]",
				[]string{"A positional selector is an exact client name. File output is never overwritten."},
				"ovpn client export laptop -o laptop.ovpn"),
			leaf("rename", "rename a client without changing its UUID", "ovpn client rename (NAME|--name|-n NAME|--id|-i ID) NEW_NAME",
				[]string{"A positional selector is an exact client name; ID prefixes require --id or -i."},
				"ovpn client rename laptop office-laptop"),
			leaf("revoke", "revoke a client certificate", "ovpn client revoke (NAME|--name|-n NAME|--id|-i ID) [--release-ipv4|-4]",
				[]string{"Static IPv4 is retained by default. Use -4 to release it during revocation."},
				"ovpn client revoke laptop", "ovpn client revoke laptop -4"),
			leaf("reissue", "reissue a client certificate and profile", "ovpn client reissue (NAME|--name|-n NAME|--id|-i ID) [--ipv4|-4 [auto|dynamic|ADDRESS]]",
				[]string{"IPv4 intent is retained when omitted. A bare -4 changes it to auto."},
				"ovpn client reissue laptop", "ovpn client reissue laptop -4 dynamic"),
			leaf("delete", "delete local client credentials and retain a UUID tombstone", "ovpn client delete (NAME|--name|-n NAME|--id|-i ID) [--yes|-y]",
				[]string{"Requires interactive confirmation unless --yes or -y is supplied."},
				"ovpn client delete laptop", "ovpn client delete laptop -y"),
			group("address", "manage client IPv4 assignment intent",
				leaf("set", "set one active client's IPv4 intent", "ovpn client address set (NAME|--name|-n NAME|--id|-i ID) --ipv4|-4 [auto|dynamic|ADDRESS]",
					[]string{"A bare --ipv4 or -4 means auto, the lowest available static address."},
					"ovpn client address set laptop -4 dynamic"),
				leaf("edit", "edit multiple client IPv4 assignments atomically", "ovpn client address edit (--all|-a|NAME...|--name|-n NAME...|--id|-i ID...) [--yes|-y]",
					[]string{"Opens a private CSV in OVPN_EDITOR, EDITOR, or the default editor. Requires confirmation."},
					"ovpn client address edit laptop phone", "ovpn client address edit -a -y"),
				leaf("release", "release a revoked client's retained static IPv4", "ovpn client address release (NAME|--name|-n NAME|--id|-i ID)",
					[]string{"The selected client must be revoked and still retain a static assignment."},
					"ovpn client address release laptop"))),
		group("state", "inspect authoritative instance state",
			leaf("show", "show aggregate instance state", "ovpn state show [--json|-j]",
				[]string{"Read-only; summarizes schema and issue count."},
				"ovpn state show"),
			leaf("doctor", "diagnose SQLite, PKI, and artifact consistency", "ovpn state doctor [--json|-j]",
				[]string{"Read-only; critical or unrecoverable state returns exit code 78."},
				"ovpn state doctor", "ovpn state doctor -j")),
		group("repair", "plan or apply state repair",
			leaf("plan", "plan safe repairs and report blockers", "ovpn repair plan [--json|-j]",
				[]string{"Read-only; authority is never reconstructed from guesses."},
				"ovpn repair plan"),
			leaf("apply", "apply eligible repairs transactionally", "ovpn repair apply [--yes|-y] [--json|-j]",
				[]string{"Requires confirmation unless --yes or -y is supplied."},
				"ovpn repair apply", "ovpn repair apply -y")),
		group("migrate", "plan or apply schema 3 migration",
			leaf("plan", "plan an offline schema 3 to 4 migration", "ovpn migrate plan [--json|-j]",
				[]string{"Read-only; schema 1 or 2 must first be upgraded with sh-ver."},
				"ovpn migrate plan"),
			leaf("apply", "migrate schema 3 state to SQLite schema 4", "ovpn migrate apply [--yes|-y] [--json|-j]",
				[]string{"Requires OVPN_MAINTENANCE=true, a stopped server, and confirmation."},
				"ovpn migrate apply -y")),
		group("runtime", "inspect the running OpenVPN service",
			leaf("status", "show daemon and connected-client status", "ovpn runtime status [--json|-j]",
				[]string{"Requires the running management broker."},
				"ovpn runtime status"),
			leaf("health", "check broker and OpenVPN health", "ovpn runtime health",
				[]string{"Prints healthy only when the runtime responds successfully."},
				"ovpn runtime health"),
			leaf("capabilities", "inspect OpenVPN compatibility", "ovpn runtime capabilities [--json|-j]",
				[]string{"Checks the pinned compatibility contract against the installed OpenVPN binary."},
				"ovpn runtime capabilities"),
			leaf("logs", "read or follow persistent OpenVPN logs", "ovpn runtime logs [--lines|-l N] [--follow|-f] [--raw|-r] [--full-id|-u]",
				[]string{"Lines default to 100. Use 0 with --follow to show only new lines."},
				"ovpn runtime logs -l 200", "ovpn runtime logs -l 0 -f"),
			leaf("events", "read or follow user-facing runtime events", "ovpn runtime events [--lines|-l N] [--follow|-f] [--json|-j] [--full-id|-u]",
				[]string{"Lines default to 100. JSON mode emits one JSON object per line."},
				"ovpn runtime events -l 200 -j")),
		leaf("version", "print build and schema version", "ovpn version [--short|-s|--json|-j]",
			[]string{"--short prints only the project version; --json emits a stable object."},
			"ovpn version", "ovpn version -s", "ovpn version -j"),
	},
}

func group(name, summary string, children ...command) command {
	return command{name: name, summary: summary, children: children}
}

func leaf(name, summary, usage string, details []string, examples ...string) command {
	return command{name: name, summary: summary, usage: usage, details: details, examples: examples}
}

func (c command) child(name string) (command, bool) {
	for _, child := range c.children {
		if child.name == name {
			return child, true
		}
	}
	return command{}, false
}

func findCommand(path []string) (command, bool) {
	current := rootCommand
	for _, name := range path {
		next, found := current.child(name)
		if !found {
			return command{}, false
		}
		current = next
	}
	return current, true
}

func writeHelp(writer io.Writer, path []string) {
	current, found := findCommand(path)
	if !found {
		current = rootCommand
		path = nil
	}
	usage := current.usage
	if usage == "" {
		usage = "ovpn"
		if len(path) > 0 {
			usage += " " + strings.Join(path, " ")
		}
		if len(current.children) > 0 {
			usage += " <command>"
		}
	}
	fmt.Fprintf(writer, "Usage: %s\n", usage)
	if current.summary != "" {
		fmt.Fprintf(writer, "\n%s.\n", current.summary)
	}
	if len(current.details) > 0 {
		fmt.Fprintln(writer, "\nDetails:")
		for _, detail := range current.details {
			fmt.Fprintf(writer, "  %s\n", detail)
		}
	}
	if len(current.children) > 0 {
		fmt.Fprintln(writer, "\nCommands:")
		for _, child := range current.children {
			fmt.Fprintf(writer, "  %-14s %s\n", child.name, child.summary)
		}
	}
	if len(current.examples) > 0 {
		fmt.Fprintln(writer, "\nExamples:")
		for _, example := range current.examples {
			fmt.Fprintf(writer, "  %s\n", example)
		}
	}
}
