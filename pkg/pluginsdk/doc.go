// Package pluginsdk is the Go SDK for Goblin Gate plugins.
//
// A plugin is a program goblind starts as goblin-plugin@<name>.service, a
// throwaway unprivileged user with its own state directory. It talks to
// goblind over a Unix socket with plain HTTP: goblind knows which plugin is
// calling from the socket peer's systemd unit, so there is no token to keep.
// Every route needs a capability the admin approved (see docs/dev/plugins.md).
//
// The smallest plugin:
//
//	func main() {
//		pluginsdk.Run(func(ctx context.Context, p *pluginsdk.Plugin) error {
//			p.Logger.Info("hello", "greeting", p.Self.String("greeting", "hi"))
//			<-ctx.Done()
//			return nil
//		})
//	}
//
// Run reads the environment goblind sets, logs to stderr (the journal), waits
// for goblind to answer, loads the plugin's approved capabilities and
// settings, and cancels ctx on SIGTERM. goblind restarts a plugin whenever its
// settings or capabilities change, so a plugin reads them once, at start.
//
// # Stability
//
// The SDK follows the plugin API, which is versioned with goblind: a plugin
// sets min_goblind in its manifest to the first goblind it needs. Within one
// major version of this package (Version), calls and types are only added,
// never changed or removed. Response types carry the fields plugins use;
// goblind may send more.
package pluginsdk

// Version is the SDK's major version.
const Version = 1
