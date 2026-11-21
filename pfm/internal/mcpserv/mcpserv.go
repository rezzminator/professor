// Package mcpserv exposes the pfm chat family over MCP: the shared daemon's
// streamable HTTP routes and each chat's stdio server.
//
// Entry points:
//   - NewConfigured, Service, ToolNames: the chat tools and their roster.
//   - NewFamilyServers, FamilyServers.RunStdio: the family servers and a chat's stdio server.
//   - NewDaemonHandler, ProbeDaemon, DaemonReachability: the daemon's routes and its health probe.
//   - Handover, OwnHandover: a stdio server's move onto a newly installed pfm.
//
// actions.go runs the chat verbs a tool calls (last, status, new, open, name, …).
// backend.go resolves chats and engines over the fleet runtime the tools read.
// call.go delivers one call to its targets for the caller.
// daemon.go serves the daemon's routes and its /status document.
// daemon_status.go probes a running daemon's health (DaemonStatus).
// families.go builds the enabled family servers and their HTTP handlers.
// herdr.go addresses a chat row's socket, native Herdr or tmux.
// httpserv.go exposes one server over MCP streamable HTTP.
// proxy.go forwards a chat's stdio frames to the daemon, in-process when it cannot.
// proxy_recovery.go replays the handshake when the daemon lost the session, and answers failures.
// proxy_requests.go orders, admits and cancels the requests the proxy forwards.
// proxyid.go carries the proxy's explicit caller identity to the daemon.
// route_probe.go catches a daemon whose family route predates its current path.
// runtime_identity.go names the fleet runtime a service was built on.
// server.go builds the chat service, its routing text and its tools.
// stdio.go runs a chat's stdio server, handing it over once an install replaced it.
// stdio_handover.go decides and starts that hand-over to the installed pfm.
// stdio_handover_child.go carries frames to and from the installed pfm serving the chat.
// stdio_handover_requests.go routes a cancellation to the process holding its request.
// types.go holds the tools' input and output shapes.
package mcpserv
