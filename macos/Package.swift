// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "FlockBar",
    platforms: [.macOS(.v15)],
    dependencies: [
        // The app's updates (Updater.swift); build-app.sh embeds and signs the framework.
        .package(url: "https://github.com/sparkle-project/Sparkle", exact: "2.10.0"),
    ],
    targets: [
        // Models and the API client, kept free of UI so they can be unit tested.
        .target(name: "FlockKit"),
        // The menu bar app's views and stores; FlockBar is only its entry point.
        .target(name: "FlockUI", dependencies: ["FlockKit", .product(name: "Sparkle", package: "Sparkle")]),
        .executableTarget(name: "FlockBar", dependencies: ["FlockKit", "FlockUI"]),
        // The app's windows with demo data, as PNGs for the website's docs. Never part of Flock.app
        // (build-app.sh builds FlockBar only and checks that).
        .executableTarget(name: "FlockScreenshots", dependencies: ["FlockUI"]),
        .testTarget(name: "FlockKitTests", dependencies: ["FlockKit"]),
    ]
)
