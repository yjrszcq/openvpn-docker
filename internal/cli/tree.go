package cli

import (
	"fmt"
	"io"
	"strings"
)

type command struct {
	name     string
	summary  string
	children []command
}

var rootCommand = command{
	name: "ovpn",
	children: []command{
		{name: "server", summary: "initialize, run, or render the OpenVPN server", children: []command{
			{name: "init", summary: "initialize a schema 4 instance"},
			{name: "run", summary: "supervise OpenVPN and its broker"},
			{name: "render", summary: "render the server configuration"},
		}},
		{name: "config", summary: "validate, inspect, plan, or apply declarative configuration", children: leaves("validate", "show", "export", "plan", "apply")},
		{name: "client", summary: "manage clients and addresses", children: []command{
			{name: "create"}, {name: "list"}, {name: "export"}, {name: "rename"},
			{name: "revoke"}, {name: "reissue"}, {name: "delete"},
			{name: "address", children: leaves("set", "edit", "release")},
		}},
		{name: "state", summary: "inspect authoritative instance state", children: leaves("show", "doctor")},
		{name: "repair", summary: "plan or apply state repair", children: leaves("plan", "apply")},
		{name: "migrate", summary: "plan or apply schema 3 migration", children: leaves("plan", "apply")},
		{name: "runtime", summary: "inspect the running service", children: leaves("status", "health", "capabilities", "logs", "events")},
		{name: "version", summary: "print build and schema version"},
	},
}

func leaves(names ...string) []command {
	commands := make([]command, 0, len(names))
	for _, name := range names {
		commands = append(commands, command{name: name})
	}
	return commands
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
	usage := "ovpn"
	if len(path) > 0 {
		usage += " " + strings.Join(path, " ")
	}
	if len(current.children) > 0 {
		usage += " <command>"
	}
	fmt.Fprintf(writer, "Usage: %s [options]\n", usage)
	if current.summary != "" {
		fmt.Fprintf(writer, "\n%s.\n", current.summary)
	}
	if len(current.children) > 0 {
		fmt.Fprintln(writer, "\nCommands:")
		for _, child := range current.children {
			fmt.Fprintf(writer, "  %-14s %s\n", child.name, child.summary)
		}
	}
}
