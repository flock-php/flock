import AppKit

/// Drives the app's views the way a user would, from inside the process: lists through their
/// table views, buttons by clicking their text, menus through their pop-up buttons.
@MainActor
enum UI {
    /// Time for a window's first loads from the fake daemon and its layout.
    static func settle() async {
        try? await Task.sleep(for: .milliseconds(700))
    }

    // MARK: Lists

    /// Selects a row of the window's first list that has that many rows (SwiftUI lists are table views).
    static func selectRow(in window: NSWindow, index: Int, of count: Int? = nil) throws {
        let table = views(of: NSTableView.self, in: window).first { table in
            if let count { table.numberOfRows == count } else { table.numberOfRows > index }
        }
        guard let table else {
            throw CaptureError("no list has a row \(index)")
        }
        table.selectRowIndexes([index], byExtendingSelection: false)
        window.makeFirstResponder(table)
    }

    /// Scrolls the window's scroll view that has something to scroll to its end.
    static func scrollToEnd(_ window: NSWindow) throws {
        guard let scrollView = views(of: NSScrollView.self, in: window).first(where: { scrollView in
            (scrollView.documentView?.frame.height ?? 0) > scrollView.contentView.bounds.height + 1
        }), let document = scrollView.documentView else {
            throw CaptureError("nothing to scroll")
        }
        document.scroll(NSPoint(x: 0, y: document.isFlipped ? document.frame.height : 0))
        scrollView.reflectScrolledClipView(scrollView.contentView)
    }

    // MARK: Buttons and fields

    /// Clicks the text (a button's title or a link) where the window shows it. SwiftUI's buttons are
    /// AppKit buttons without a title, and SwiftUI keeps its accessibility elements to itself
    /// unless an assistive app asks (which needs a permission), so the text is read off the window.
    static func click(in window: NSWindow, text: String) throws {
        guard let rect = try ScreenText.find(text, in: window) else {
            throw CaptureError("the window doesn't show \"\(text)\"")
        }
        // The text is drawn by a view of its own, over the button.
        let point = NSPoint(x: rect.midX, y: rect.midY)
        let control = views(of: NSControl.self, in: window)
            .filter { $0.convert($0.bounds, to: nil).contains(point) }
            .min { $0.bounds.width * $0.bounds.height < $1.bounds.width * $1.bounds.height }
        guard let control else {
            throw CaptureError("\"\(text)\" isn't a button")
        }
        control.performClick(nil)
    }

    /// Types text into the text field of the window's sheet (an alert asking for a name).
    static func type(_ text: String, inSheetOf window: NSWindow) async throws {
        for _ in 0..<30 {
            if let sheet = window.attachedSheet, let field = views(of: NSTextField.self, in: sheet).first(where: \.isEditable) {
                sheet.makeFirstResponder(field)
                field.stringValue = text
                field.currentEditor()?.selectedRange = NSRange(location: text.utf16.count, length: 0)
                await settle()
                return
            }
            try await Task.sleep(for: .milliseconds(100))
        }
        throw CaptureError("no sheet with a text field")
    }

    // MARK: Menus

    /// Opens the pop-up menu of the window that has an item with this text in its title. A
    /// SwiftUI Menu builds its items when it opens, so pop-ups without items are tried one by one.
    /// The menu tracks the mouse in a loop of its own; the screenshot is taken meanwhile and
    /// closeMenus ends it.
    static func openMenu(in window: NSWindow, containing item: String) async throws {
        func matches(_ menu: NSMenu?) -> Bool { menu?.items.contains { $0.title.contains(item) } ?? false }
        let buttons = views(of: NSPopUpButton.self, in: window)
        startObservingMenus()
        for button in buttons.filter({ matches($0.menu) }) + buttons.filter({ $0.menu?.items.isEmpty ?? true }) {
            // Not on the main queue: the menu's loop would block it, and with it this task.
            RunLoop.main.perform(inModes: [.default]) {
                MainActor.assumeIsolated { button.performClick(nil) }
            }
            for _ in 0..<30 where openMenus.isEmpty {
                try await Task.sleep(for: .milliseconds(100))
            }
            if openMenus.contains(where: matches) {
                await settle()
                return
            }
            closeMenus()
            for _ in 0..<30 where !openMenus.isEmpty {
                try await Task.sleep(for: .milliseconds(100))
            }
        }
        throw CaptureError("no menu with \"\(item)\"")
    }

    private static var openMenus: [NSMenu] = []
    private static var observing = false

    private static func startObservingMenus() {
        guard !observing else { return }
        observing = true
        // Menus post these on the main thread.
        NotificationCenter.default.addObserver(forName: NSMenu.didBeginTrackingNotification, object: nil, queue: nil) { note in
            nonisolated(unsafe) let object = note.object
            MainActor.assumeIsolated {
                if let menu = object as? NSMenu {
                    openMenus.append(menu)
                }
            }
        }
        NotificationCenter.default.addObserver(forName: NSMenu.didEndTrackingNotification, object: nil, queue: nil) { note in
            nonisolated(unsafe) let object = note.object
            MainActor.assumeIsolated {
                openMenus.removeAll { $0 === object as? NSMenu }
            }
        }
    }

    static func closeMenus() {
        for menu in openMenus {
            menu.cancelTrackingWithoutAnimation()
        }
    }

    // MARK: Finding things

    static func views<T: NSView>(of type: T.Type, in window: NSWindow) -> [T] {
        guard let root = window.contentView?.superview ?? window.contentView else { return [] }
        return views(of: type, in: root)
    }

    private static func views<T: NSView>(of type: T.Type, in view: NSView) -> [T] {
        ((view as? T).map { [$0] } ?? []) + view.subviews.flatMap { views(of: type, in: $0) }
    }
}
