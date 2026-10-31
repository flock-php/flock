import AppKit
import Observation
@preconcurrency import Sparkle

/// The app's own updates, through Sparkle. It checks the appcast (SUFeedURL in build-app.sh) once a
/// day and installs a new version once the user confirms; the relaunched app then updates the CLI
/// and the daemon (AppModel.updateInstallation). Betas are offered while the daemon follows the beta
/// channel, and to a beta on any channel until the release it leads to. Development builds (CFBundleVersion 0, no SUPublicEDKey) don't update.
@MainActor
@Observable
final class Updater: NSObject {
    /// A release build that can update itself.
    let isEnabled: Bool
    /// The newer version the last check found, e.g. "1.1.0", until a check finds none.
    private(set) var availableVersion: String?
    /// A check the user started is running.
    private(set) var isChecking = false
    /// The last check found no newer version.
    private(set) var upToDate = false

    /// UserDefaults key: the last channel the daemon reported.
    private static let channelKey = "updateChannel"

    /// The daemon's channel (Status.channel), or the last one seen while it isn't running. A change
    /// (the Beta versions toggle) checks again.
    @ObservationIgnored var channel = UserDefaults.standard.string(forKey: Updater.channelKey) ?? "stable" {
        didSet {
            guard channel != oldValue else { return }
            UserDefaults.standard.set(channel, forKey: Self.channelKey)
            checkInBackground()
        }
    }

    @ObservationIgnored private var controller: SPUStandardUpdaterController?

    override init() {
        let info = Bundle.main.infoDictionary ?? [:]
        let build = info["CFBundleVersion"] as? String ?? "0"
        let feed = info["SUFeedURL"] as? String ?? ""
        let key = info["SUPublicEDKey"] as? String ?? ""
        isEnabled = build != "0" && !feed.isEmpty && !key.isEmpty
        super.init()
    }

    /// The app's version, e.g. "1.1.0" or "1.2.0-beta.1".
    static var appVersion: String {
        let info = Bundle.main.infoDictionary
        return info?["CFBundleShortVersionString"] as? String ?? info?["CFBundleVersion"] as? String ?? "dev"
    }

    /// Starts the daily checks.
    func start() {
        guard isEnabled, controller == nil else { return }
        controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: self, userDriverDelegate: self)
    }

    /// Checks now and shows the result in Sparkle's window: the update with its release notes, or
    /// that Flock is up to date. With an update already found, it shows that one.
    func checkForUpdates() {
        guard let controller else { return }
        isChecking = true
        NSApp.activate()
        controller.checkForUpdates(nil)
    }

    /// Checks without a window; a newer version shows in the panel and in Settings.
    func checkInBackground() {
        guard let updater = controller?.updater, updater.canCheckForUpdates else { return }
        updater.checkForUpdatesInBackground()
    }
}

extension Updater: SPUUpdaterDelegate {
    /// Beta items (`<sparkle:channel>beta`) on the beta and experimental channels, and while a beta
    /// runs; stable items always.
    func allowedChannels(for updater: SPUUpdater) -> Set<String> {
        channel == "beta" || channel == "experimental" || Self.appVersion.contains("-beta.") ? ["beta"] : []
    }

    func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        availableVersion = item.displayVersionString
        upToDate = false
        isChecking = false
    }

    func updaterDidNotFindUpdate(_ updater: SPUUpdater) {
        availableVersion = nil
        upToDate = true
        isChecking = false
    }

    func updater(_ updater: SPUUpdater, didFinishUpdateCycleFor updateCheck: SPUUpdateCheck, error: (any Error)?) {
        isChecking = false
    }
}

extension Updater: @preconcurrency SPUStandardUserDriverDelegate {
    /// A menu bar app is rarely in front: an update found by the daily check shows in the panel and
    /// in Settings rather than in a window of its own, unless Flock is being used right then.
    var supportsGentleScheduledUpdateReminders: Bool { true }

    func standardUserDriverShouldHandleShowingScheduledUpdate(_ update: SUAppcastItem, andInImmediateFocus immediateFocus: Bool) -> Bool {
        immediateFocus
    }
}
