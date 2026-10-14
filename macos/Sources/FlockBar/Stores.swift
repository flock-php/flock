import AppKit
import FlockKit
import Observation
import UniformTypeIdentifiers

/// Caught mail for the Mail window.
@MainActor
@Observable
final class MailStore {
    private let client: FlockClient

    var messages: [MailSummary] = []
    var error: String?
    var isVisible = false

    init(client: FlockClient) {
        self.client = client
    }

    func refresh() async {
        do {
            // Assigning an equal array still makes the list reload mid-update; only publish real changes.
            let fresh = try await client.mail()
            if fresh != messages {
                messages = fresh
            }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    func refreshIfVisible() async {
        if isVisible {
            await refresh()
        }
    }

    func detail(_ id: String) async throws -> MailDetail {
        try await client.mail(id: id)
    }

    func raw(_ id: String) async throws -> String {
        try await client.rawMail(id: id)
    }

    func clear() async {
        do {
            try await client.clearMail()
            messages = []
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Asks where to save an attachment, then downloads it there.
    func save(_ attachment: MailDetail.Attachment, of mailID: String) async {
        let panel = NSSavePanel()
        panel.nameFieldStringValue = attachment.filename.isEmpty ? "attachment" : attachment.filename
        if let type = UTType(mimeType: attachment.contentType) {
            panel.allowedContentTypes = [type]
        }
        guard panel.runModal() == .OK, let url = panel.url else { return }

        do {
            let data = try await client.attachment(mailID: mailID, index: attachment.index)
            try data.write(to: url)
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// Captured dump()/dd() output for the Dumps window.
@MainActor
@Observable
final class DumpStore {
    private let client: FlockClient

    var dumps: [Dump] = []
    var header = ""
    var error: String?
    var isVisible = false

    init(client: FlockClient) {
        self.client = client
    }

    func refresh() async {
        do {
            let fresh = try await client.dumps()
            // The header only exists once the dump server has run; fetch it when first needed.
            if header.isEmpty, !fresh.isEmpty {
                header = (try? await client.dumpHeader()) ?? ""
            }
            if fresh != dumps {
                dumps = fresh
            }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    func refreshIfVisible() async {
        if isVisible {
            await refresh()
        }
    }

    func clear() async {
        do {
            try await client.clearDumps()
            dumps = []
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// Site (Laravel) and service logs for the Logs window.
@MainActor
@Observable
final class LogStore {
    enum Source: Hashable {
        case site(String)
        case service(String)
    }

    private let client: FlockClient

    var index = LogIndex()
    var entries: [LogEntry] = []
    var serviceText = ""
    /// The source `entries` / `serviceText` belong to; another one is still loading.
    private(set) var loaded: Source?
    var error: String?

    init(client: FlockClient) {
        self.client = client
    }

    func loadIndex() async {
        do {
            let fresh = try await client.logs()
            if fresh != index {
                index = fresh
            }
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// Loads a source. A load that was cancelled (another source was picked meanwhile) changes nothing.
    func load(_ source: Source) async {
        do {
            switch source {
            case let .site(name):
                let fresh = try await client.siteLog(name)
                try Task.checkCancellation()
                if fresh != entries || loaded != source {
                    entries = fresh
                }
            case let .service(name):
                let fresh = try await client.serviceLog(name)
                try Task.checkCancellation()
                if fresh != serviceText || loaded != source {
                    serviceText = fresh
                }
            }
            loaded = source
            error = nil
        } catch where Task.isCancelled {
        } catch {
            if loaded != source {
                // Don't leave the previous source's lines under this one's name.
                entries = []
                serviceText = ""
                loaded = source
            }
            self.error = error.localizedDescription
        }
    }

    func clear(site: String) async {
        do {
            try await client.clearSiteLog(site)
            entries = []
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}
