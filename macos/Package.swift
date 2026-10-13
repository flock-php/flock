// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "FlockBar",
    platforms: [.macOS(.v15)],
    targets: [
        // Models and the API client, kept free of UI so they can be unit tested.
        .target(name: "FlockKit"),
        .executableTarget(name: "FlockBar", dependencies: ["FlockKit"]),
        .testTarget(name: "FlockKitTests", dependencies: ["FlockKit"]),
    ]
)
