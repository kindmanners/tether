// Package scripts exposes the platform node bootstrappers to the desktop Agent.
// Keeping the embedded bytes beside the executable script ensures the GUI and
// terminal flow always execute the same reviewed provisioning code.
package scripts

import _ "embed"

// WindowsNodeBootstrap is materialized into the user's local Tether install
// directory only while the Agent needs to run elevated setup.
//
//go:embed bootstrap-windows-node.ps1
var WindowsNodeBootstrap []byte

// LinuxNodeBootstrap is materialized into the user's local cache directory
// while the Agent runs the reviewed Linux setup. It deliberately never handles
// pairing or writes any state outside the local Tether directories it receives.
//
//go:embed bootstrap-linux-node.sh
var LinuxNodeBootstrap []byte
