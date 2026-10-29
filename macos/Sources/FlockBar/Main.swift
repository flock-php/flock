import FlockUI
import Foundation

/// Entry point: `FlockBar --selftest` checks every API call against the running daemon
/// and exits; otherwise the menu bar app starts.
@main
enum Main {
    static func main() {
        if CommandLine.arguments.contains("--selftest") {
            exit(SelfTest.run())
        }
        FlockBarApp.main()
    }
}
