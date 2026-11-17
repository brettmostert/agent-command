import Foundation

/// What a session runs. The raw value is its name to the daemon.
enum Harness: String, CaseIterable, Identifiable {
    case claude
    case antigravity
    case terminal

    /// What a plain new session starts (⌘N). Named so it could become a setting.
    static let defaultForNew = Harness.claude

    var id: String { rawValue }

    var title: String {
        switch self {
        case .claude: "Claude"
        case .antigravity: "Antigravity"
        case .terminal: "Terminal"
        }
    }

    /// The folder panel's button.
    var panelPrompt: String { self == .terminal ? "Open Terminal" : "Start Session" }

    /// A session's harness. Older sessions have none, and are Claude's.
    init?(session name: String) {
        self.init(rawValue: name.isEmpty ? Harness.claude.rawValue : name)
    }
}

/// The new-session picker's list: what's typed narrows it, the arrows move along it,
/// and a number picks that row straight away.
struct HarnessChoice {
    let harnesses: [Harness]
    private(set) var search = ""
    private(set) var selected = 0

    init(_ harnesses: [Harness] = Harness.allCases) {
        self.harnesses = harnesses
    }

    var matches: [Harness] {
        let query = search.trimmingCharacters(in: .whitespaces)
        return query.isEmpty ? harnesses : harnesses.filter { $0.title.localizedCaseInsensitiveContains(query) }
    }

    /// The row's number, as shown and typed.
    func number(of harness: Harness) -> Int { (harnesses.firstIndex(of: harness) ?? 0) + 1 }

    /// What return picks.
    var chosen: Harness? {
        let list = matches
        return list.indices.contains(selected) ? list[selected] : nil
    }

    mutating func move(_ delta: Int) {
        let count = matches.count
        if count > 0 { selected = (selected + delta + count) % count }
    }

    mutating func select(_ harness: Harness) {
        if let index = matches.firstIndex(of: harness) { selected = index }
    }

    /// Takes what's in the search line. A row's number on its own picks that row.
    mutating func setSearch(_ text: String) -> Harness? {
        let trimmed = text.trimmingCharacters(in: .whitespaces)
        if let n = Int(trimmed), harnesses.indices.contains(n - 1) { return harnesses[n - 1] }
        search = text
        selected = 0
        return nil
    }
}
