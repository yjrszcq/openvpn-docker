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
	summary: "manage an OpenVPN instance",
	usage:   "ovpn <command> [options]",
	details: []string{
		"Run 'ovpn help COMMAND' or 'ovpn COMMAND -h' for command help.",
		"Use -v for the short version, or -V/--version for the full version report.",
		"Client, state, and runtime groups default to list, doctor, and status respectively.",
	},
	children: []command{
		group("server", "initialize, run, or render the OpenVPN server",
			leaf("init", "initialize an empty OpenVPN instance", "ovpn server init",
				[]string{"Requires an empty data directory and either valid declarative YAML or one-time bootstrap environment."},
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
			leaf("create", "create a client and its credentials", "ovpn client create NAME [--ipv4|-4 [auto|dynamic|ADDRESS]] [--output|-o FILE|-] [--full-id|-u] [--json|-j]",
				[]string{"IPv4 defaults to auto, the lowest available static address. A bare -4 also means auto.", "Profile output happens after the durable mutation. JSON cannot be combined with -o -.", "JSON output always contains full UUIDs."},
				"ovpn client create laptop", "ovpn client create phone -4 dynamic -o phone.ovpn"),
			leaf("list", "list active and revoked clients", "ovpn client list [--detail|-d] [--full-id|-u] [--json|-j]",
				[]string{"Text output uses short IDs by default; JSON always contains full UUIDs."},
				"ovpn client list", "ovpn client list -d"),
			leaf("export", "export an active client profile", "ovpn client export (NAME|--name|-n NAME|--id|-i ID) [--output|-o FILE|-]",
				[]string{"A positional selector is an exact client name. File output is never overwritten."},
				"ovpn client export laptop -o laptop.ovpn"),
			leaf("rename", "rename a client without changing its UUID", "ovpn client rename (NAME|--name|-n NAME|--id|-i ID) NEW_NAME [--full-id|-u] [--json|-j]",
				[]string{"A positional selector is an exact client name; ID prefixes require --id or -i.", "The regenerated profile must be redistributed after a real rename."},
				"ovpn client rename laptop office-laptop"),
			leaf("revoke", "revoke a client certificate", "ovpn client revoke (NAME|--name|-n NAME|--id|-i ID) [--release-ipv4|-4] [--full-id|-u] [--json|-j]",
				[]string{"Static IPv4 is retained by default. Use -4 to release it during revocation.", "The current session is disconnected after commit when the runtime is reachable."},
				"ovpn client revoke laptop", "ovpn client revoke laptop -4"),
			leaf("reissue", "reissue a client certificate and profile", "ovpn client reissue (NAME|--name|-n NAME|--id|-i ID) [--ipv4|-4 [auto|dynamic|ADDRESS]] [--output|-o FILE|-] [--full-id|-u] [--json|-j]",
				[]string{"IPv4 intent is retained when omitted. A bare -4 changes it to auto.", "Profile output and current-session disconnect happen after the durable mutation. JSON cannot be combined with -o -."},
				"ovpn client reissue laptop", "ovpn client reissue laptop -4 dynamic -o laptop.ovpn"),
			leaf("delete", "delete local client credentials and retain a UUID tombstone", "ovpn client delete (NAME|--name|-n NAME|--id|-i ID) [--yes|-y] [--full-id|-u] [--json|-j]",
				[]string{"Requires interactive confirmation unless --yes or -y is supplied.", "An active current session is disconnected after the tombstone commits."},
				"ovpn client delete laptop", "ovpn client delete laptop -y"),
			group("address", "manage client IPv4 assignment intent",
				leaf("set", "set one active client's IPv4 intent", "ovpn client address set (NAME|--name|-n NAME|--id|-i ID) --ipv4|-4 [auto|dynamic|ADDRESS] [--full-id|-u] [--json|-j]",
					[]string{"A bare --ipv4 or -4 means auto, the lowest available static address.", "The current session is disconnected after commit so the new assignment can take effect."},
					"ovpn client address set laptop -4 dynamic"),
				leaf("edit", "edit multiple client IPv4 assignments atomically", "ovpn client address edit (--all|-a|NAME...|--name|-n NAME...|--id|-i ID...) [--yes|-y] [--json|-j]",
					[]string{"Opens a private CSV using OVPN_EDITOR, then EDITOR, then nano. Requires confirmation.", "Selected active sessions are disconnected after the batch commits."},
					"ovpn client address edit laptop phone", "ovpn client address edit -a -y"),
				leaf("release", "release a revoked client's retained static IPv4", "ovpn client address release (NAME|--name|-n NAME|--id|-i ID) [--full-id|-u] [--json|-j]",
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
		group("migrate", "plan or apply persistent data migration",
			leaf("plan", "plan an offline legacy data migration", "ovpn migrate plan [--json|-j]",
				[]string{"Read-only; schema 1 or 2 must first be upgraded with sh-ver."},
				"ovpn migrate plan"),
			leaf("apply", "migrate legacy state to the current data format", "ovpn migrate apply [--yes|-y] [--json|-j]",
				[]string{"Requires OVPN_MAINTENANCE=true, a stopped server, and confirmation."},
				"ovpn migrate apply -y")),
		group("runtime", "inspect the running OpenVPN service",
			leaf("status", "show daemon and connected-client status", "ovpn runtime status [--json|-j] [--full-id|-u]",
				[]string{"Requires the running management broker."},
				"ovpn runtime status"),
			leaf("disconnect", "disconnect a client by immutable certificate identity", "ovpn runtime disconnect (NAME|--name|-n NAME|--id|-i ID) [--json|-j] [--full-id|-u]",
				[]string{"A client that is already offline is a successful no-op. Deleted tombstones can be selected with --id."},
				"ovpn runtime disconnect laptop", "ovpn runtime disconnect -i 11111111 -j"),
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
		leaf("completion", "generate shell completion for ovpn", "ovpn completion (bash|zsh|fish)",
			[]string{"Writes a dependency-free completion script to stdout. Client names and IDs are queried only while completing selector values."},
			"ovpn completion bash > /etc/bash_completion.d/ovpn", "ovpn completion zsh > ~/.zfunc/_ovpn", "ovpn completion fish > ~/.config/fish/completions/ovpn.fish"),
		leaf("version", "print build and data-format versions", "ovpn version [--short|-s|--json|-j]",
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

func writeCommandOverview(writer io.Writer) {
	fmt.Fprintf(writer, "Usage: %s\n\nCommand tree:\n", rootCommand.usage)
	writeCommandOverviewChildren(writer, rootCommand.children, "")
}

func writeCommandOverviewChildren(writer io.Writer, children []command, prefix string) {
	for index, child := range children {
		last := index == len(children)-1
		branch := "├── "
		continuation := "│   "
		if last {
			branch = "└── "
			continuation = "    "
		}
		fmt.Fprintf(writer, "%s%s%s - %s\n", prefix, branch, child.name, child.summary)
		if len(child.children) > 0 {
			writeCommandOverviewChildren(writer, child.children, prefix+continuation)
			continue
		}
		fmt.Fprintf(writer, "%s%s└── Usage: %s\n", prefix, continuation, child.usage)
	}
}
