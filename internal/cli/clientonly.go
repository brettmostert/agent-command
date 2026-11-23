package cli

import "runtime"

// clientOnly is a machine whose daemon runs no sessions of its own: it keeps
// known hosts, connects to them and passes on their sessions (Windows,
// daemon.Config.ClientOnly). Hosting sessions there isn't planned.
var clientOnly = runtime.GOOS == "windows"
