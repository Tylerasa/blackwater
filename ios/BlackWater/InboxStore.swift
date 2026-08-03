//  InboxStore.swift
//
//  Writes JSON files into the app's iCloud Drive container so the Mac's
//  `ledger ingest` command can pick them up. The Share Extension calls
//  `save(...)`; the container app calls `pendingCount()` to show a badge.
//
//  IMPORTANT — bundle identifier consistency:
//  The container ID used here MUST match the "iCloud Documents container"
//  ID configured in Signing & Capabilities → iCloud → Containers for both
//  the app target AND the extension target. If they diverge, files written
//  by the extension will NOT appear when the container app checks the
//  inbox — you'll waste an hour debugging what looks like an iCloud sync
//  bug but is actually a mismatched entitlement.
//
//  Convention: iCloud.<reverse-DNS bundle ID>.
//  If your bundle ID is com.tylerasa.blackwater, the container becomes
//  iCloud.com.tylerasa.blackwater — edit iCloudContainerID below to match.

import Foundation

enum InboxStore {
    /// The iCloud container identifier that appears in your entitlements.
    /// Adjust this to match what you configure in Xcode's Signing &
    /// Capabilities screen for both targets.
    static let iCloudContainerID = "iCloud.com.tylerasa.blackwater"

    /// One inbox message.
    struct Message: Codable {
        let sender: String
        let body: String
        let capturedAt: String   // RFC-3339 UTC — matches Go's time.RFC3339
    }

    /// Save one message to the inbox. Returns the URL of the written file
    /// so callers can log it. Errors surface up so the UI can show them.
    @discardableResult
    static func save(sender: String, body: String) throws -> URL {
        let inboxURL = try ensureInboxDir()
        let now = Date()
        let msg = Message(sender: sender, body: body, capturedAt: rfc3339(now))
        let name = filenameFor(now: now)
        let fileURL = inboxURL.appendingPathComponent(name)

        let data = try JSONEncoder().encode(msg)
        try data.write(to: fileURL, options: [.atomic])
        return fileURL
    }

    /// How many pending inbox files are on this device. Zero doesn't
    /// necessarily mean the Mac has already ingested them — iCloud sync
    /// may still be in progress or the Mac may not have polled yet.
    static func pendingCount() -> Int {
        guard let inboxURL = try? ensureInboxDir() else { return 0 }
        let fm = FileManager.default
        guard let items = try? fm.contentsOfDirectory(
            at: inboxURL,
            includingPropertiesForKeys: nil,
            options: [.skipsHiddenFiles]
        ) else { return 0 }
        return items.filter { $0.pathExtension.lowercased() == "json" }.count
    }

    // MARK: - Internals

    private static func ensureInboxDir() throws -> URL {
        let fm = FileManager.default
        guard let containerURL = fm.url(forUbiquityContainerIdentifier: iCloudContainerID) else {
            throw InboxError.iCloudUnavailable
        }
        let inbox = containerURL
            .appendingPathComponent("Documents", isDirectory: true)
            .appendingPathComponent("inbox", isDirectory: true)
        try fm.createDirectory(at: inbox, withIntermediateDirectories: true)
        return inbox
    }

    private static func filenameFor(now: Date) -> String {
        let df = DateFormatter()
        df.locale = Locale(identifier: "en_US_POSIX")
        df.timeZone = TimeZone(identifier: "UTC")
        df.dateFormat = "yyyyMMdd'T'HHmmss'Z'"
        let stamp = df.string(from: now)
        let uniq = UUID().uuidString.prefix(8)
        return "\(stamp)-\(uniq).json"
    }

    private static func rfc3339(_ d: Date) -> String {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f.string(from: d)
    }
}

enum InboxError: LocalizedError {
    case iCloudUnavailable

    var errorDescription: String? {
        switch self {
        case .iCloudUnavailable:
            return "iCloud Drive is not available. Enable iCloud Drive in Settings and make sure BlackWater is switched on."
        }
    }
}
