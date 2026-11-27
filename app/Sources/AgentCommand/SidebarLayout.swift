import Foundation

/// How this app's sidebar lists sessions: its own view (D2), kept in
/// UserDefaults. The daemon sends what contains what; this decides the order.
nonisolated struct SidebarOptions: Codable, Equatable {
    enum Mode: String, Codable, CaseIterable {
        case groups, list

        var title: String { self == .groups ? "Groups" : "List" }
    }

    enum Sort: String, Codable, CaseIterable {
        case created, name, activity, status, folder

        var title: String {
            switch self {
            case .created: "Created"
            case .name: "Name"
            case .activity: "Activity"
            case .status: "Status"
            case .folder: "Folder"
            }
        }
    }

    var mode = Mode.groups
    var sort = Sort.created
    var descending = false
    /// The groups collapsed to their header.
    var collapsed: Set<String> = []
    /// The group you focused: the others are collapsed. nil: none is.
    var focused: String?
    /// What was collapsed before you focused a group, to bring back after.
    var beforeFocus: Set<String>?

    /// Focuses a group: it's open, and the other groups are collapsed. Focusing
    /// another keeps what was collapsed before the first.
    mutating func focus(_ key: String, groups: [SessionGroup]) {
        if focused == nil { beforeFocus = collapsed }
        focused = key
        mode = .groups
        collapsed = Set(groups.map(\.key)).subtracting([key])
    }

    /// Leaves focus, bringing back what was collapsed before. Groups made while
    /// focused were open before, so they open.
    mutating func clearFocus() {
        guard focused != nil else { return }
        collapsed = beforeFocus ?? []
        focused = nil
        beforeFocus = nil
    }

    /// Leaves focus when the focused group is gone: deleted, or archived.
    mutating func keepFocus(among live: [SessionGroup]) {
        if let focused, !live.contains(where: { $0.key == focused }) { clearFocus() }
    }

    static let key = "sidebarView"

    static func load(_ defaults: UserDefaults = .standard) -> SidebarOptions {
        guard let data = defaults.data(forKey: key), let saved = try? JSONDecoder().decode(SidebarOptions.self, from: data) else {
            return SidebarOptions()
        }
        return saved
    }

    func save(_ defaults: UserDefaults = .standard) {
        if let data = try? JSONEncoder().encode(self) { defaults.set(data, forKey: Self.key) }
    }
}

/// A group's header in the sidebar.
nonisolated struct GroupHeader: Equatable {
    let group: SessionGroup
    /// The sessions listed in it.
    let count: Int
    /// What in it needs you most, shown while it's collapsed.
    let urgent: SessionAttention?
    let collapsed: Bool
}

/// The quiet line over a host's sessions in no group. It isn't a group: it has
/// no menu, colour or collapse, and the cursor passes it by.
nonisolated struct NoGroupHeader: Equatable {
    /// "" for this Mac.
    let host: String
    let count: Int

    var title: String { SessionGroup.noGroupTitle(host: host) }
    /// What dropping sessions on it names: no group, on its host.
    var dropKey: String { SidebarLayout.noGroupKey(host) }
}

/// A group in the sidebar's Archived section, or no group, with its archived sessions.
nonisolated struct ArchivedSection: Equatable, Identifiable {
    /// nil: sessions in no group.
    let group: SessionGroup?
    /// The host of the sessions in no group ("" for this Mac).
    var host = ""
    let sessions: [Session]

    var id: String { group?.key ?? SidebarLayout.noGroupKey(host) }
    var title: String { group?.title ?? SessionGroup.noGroupTitle(host: host) }
}

/// One line of the sidebar.
nonisolated enum SidebarRow: Equatable, Identifiable {
    case header(GroupHeader)
    case noGroup(NoGroupHeader)
    case session(Session, link: SetLink)

    var id: String {
        switch self {
        case .header(let header): "group:" + header.group.key
        case .noGroup(let header): header.dropKey
        case .session(let session, _): session.id
        }
    }

    var session: Session? {
        if case .session(let session, _) = self { return session }
        return nil
    }
}

/// Lays out the sidebar from the daemon's structure and this app's options. It
/// has no dependencies, so it can be checked on its own.
enum SidebarLayout {
    /// The rows: in group view, once there's a group or a host, each machine's
    /// group headers, then a quiet "No group" line over its sessions in no
    /// group. This Mac first, then each host. The listed sessions are sorted,
    /// with each set's together in its order. A collapsed group shows only its
    /// header. While filtering, groups with nothing listed are left out.
    static func rows(sessions: [Session], groups: [SessionGroup], sets: [[String]], options: SidebarOptions,
                     filtering: Bool = false) -> [SidebarRow] {
        let sorted = sort(sessions, by: options.sort, descending: options.descending)
        let ordered = orderedGroups(groups)
        let hosts = machines(sorted.map(\.hostName) + ordered.map(\.host))
        guard options.mode == .groups, !ordered.isEmpty || hosts.count > 1 else { return sessionRows(sorted, sets: sets) }

        // Each session in its group, on its own host. One whose group isn't
        // known is in no group.
        let known = Set(ordered.map(\.key))
        var byGroup: [String: [Session]] = [:]
        for session in sorted {
            let key = session.groupKey.flatMap { known.contains($0) ? $0 : nil } ?? noGroupKey(session.hostName)
            byGroup[key, default: []].append(session)
        }
        var rows: [SidebarRow] = []
        for host in hosts {
            for group in ordered where group.host == host {
                let members = byGroup[group.key] ?? []
                if filtering && members.isEmpty { continue }
                let collapsed = options.collapsed.contains(group.key) && !filtering
                rows.append(.header(GroupHeader(group: group, count: members.count, urgent: mostUrgent(members), collapsed: collapsed)))
                if !collapsed { rows += sessionRows(members, sets: sets) }
            }
            if let loose = byGroup[noGroupKey(host)], !loose.isEmpty {
                rows.append(.noGroup(NoGroupHeader(host: host, count: loose.count)))
                rows += sessionRows(loose, sets: sets)
            }
        }
        return rows
    }

    /// The machines, this Mac ("") first, then each host by name.
    private static func machines(_ names: [String]) -> [String] {
        Array(Set(names)).sorted { a, b in a.isEmpty || (!b.isEmpty && a < b) }
    }

    /// The key that names no group on a host, for drops and the Archived section.
    nonisolated static func noGroupKey(_ host: String) -> String { "nogroup:" + host }

    /// The host whose no group key this is, if it's one.
    nonisolated static func noGroupHost(_ key: String) -> String? { key.hasPrefix("nogroup:") ? String(key.dropFirst(8)) : nil }

    /// The sessions you can see, top to bottom: what ⌘1-9 go by.
    static func sessionIDs(_ rows: [SidebarRow]) -> [String] { rows.compactMap(\.session?.id) }

    /// What the cursor stops on, top to bottom: group headers and sessions.
    /// It passes "No group" by: there's nothing to do on it.
    static func cursorIDs(_ rows: [SidebarRow]) -> [String] {
        rows.compactMap { row in
            if case .noGroup = row { return nil }
            return row.id
        }
    }

    /// The cursor's id for a group's header.
    static func headerID(_ key: String) -> String { "group:" + key }

    /// The group whose header has this cursor id, if it's a header's.
    static func groupKey(cursor id: String) -> String? { id.hasPrefix("group:") ? String(id.dropFirst(6)) : nil }

    /// The group ⌘N starts a session in: the group you selected, else the
    /// selected session's (a session in no group means no group), else the one
    /// you focused. nil is no group.
    static func newSessionGroup(selectedGroup: String?, selected: Session?, focused: String?,
                                groups: [SessionGroup]) -> SessionGroup? {
        let live = orderedGroups(groups)
        func group(_ key: String?) -> SessionGroup? { key.flatMap { key in live.first { $0.key == key } } }
        if let found = group(selectedGroup) { return found }
        if let selected { return group(selected.groupKey) }
        return group(focused)
    }

    /// The Archived section: each machine's groups with archived sessions, or
    /// archived themselves, in the groups' order, live or archived; then its
    /// archived sessions in no group. One whose group isn't known is in no
    /// group. Sessions are oldest first.
    static func archivedSections(sessions: [Session], groups: [SessionGroup]) -> [ArchivedSection] {
        let ordered = groups.sorted(by: inOrder)
        let known = Set(ordered.map(\.key))
        var byGroup: [String: [Session]] = [:]
        for session in sessions.sorted(by: { $0.id < $1.id }) {
            let key = session.groupKey.flatMap { known.contains($0) ? $0 : nil } ?? noGroupKey(session.hostName)
            byGroup[key, default: []].append(session)
        }
        var out: [ArchivedSection] = []
        for host in machines(sessions.map(\.hostName) + ordered.map(\.host)) {
            for group in ordered where group.host == host {
                let members = byGroup[group.key] ?? []
                if !members.isEmpty || group.isArchived { out.append(ArchivedSection(group: group, sessions: members)) }
            }
            if let loose = byGroup[noGroupKey(host)], !loose.isEmpty {
                out.append(ArchivedSection(group: nil, host: host, sessions: loose))
            }
        }
        return out
    }

    private static func sessionRows(_ sessions: [Session], sets: [[String]]) -> [SidebarRow] {
        let byID = Dictionary(sessions.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        return PaneSets.rows(order: sessions.map(\.id), sets: sets).compactMap { row in
            byID[row.id].map { SidebarRow.session($0, link: row.link) }
        }
    }

    /// Live groups in the order you arranged them: this Mac's, then each
    /// host's, as the host arranges them.
    static func orderedGroups(_ groups: [SessionGroup]) -> [SessionGroup] {
        groups.filter { !$0.isArchived }.sorted(by: inOrder)
    }

    private static func inOrder(_ a: SessionGroup, _ b: SessionGroup) -> Bool {
        if a.host != b.host { return a.host.isEmpty || (!b.host.isEmpty && a.host < b.host) }
        return (a.order, a.id) < (b.order, b.id)
    }

    /// The groups a session can be moved to: its own host's.
    static func groups(for session: Session, in groups: [SessionGroup]) -> [SessionGroup] {
        orderedGroups(groups).filter { $0.host == session.hostName }
    }

    static func mostUrgent(_ sessions: [Session]) -> SessionAttention? {
        sessions.compactMap(\.attention).min { $0.rank < $1.rank }
    }

    /// Sessions in the chosen order. Ties keep creation order (ids are
    /// time-ordered), whichever the direction.
    static func sort(_ sessions: [Session], by sort: SidebarOptions.Sort, descending: Bool) -> [Session] {
        sessions.sorted { a, b in
            var c = compare(a, b, by: sort)
            if descending { c = -c }
            return c != 0 ? c < 0 : a.id < b.id
        }
    }

    private static func compare(_ a: Session, _ b: Session, by sort: SidebarOptions.Sort) -> Int {
        func order<T: Comparable>(_ x: T, _ y: T) -> Int { x < y ? -1 : (x > y ? 1 : 0) }
        func names() -> Int {
            switch a.displayName.localizedStandardCompare(b.displayName) {
            case .orderedAscending: -1
            case .orderedDescending: 1
            case .orderedSame: 0
            }
        }
        switch sort {
        case .created: return 0
        case .name: return names()
        case .activity:
            let x = a.lastEventAt.flatMap(Timestamps.date) ?? .distantPast
            let y = b.lastEventAt.flatMap(Timestamps.date) ?? .distantPast
            return order(x, y)
        case .status: return order(statusRank(a), statusRank(b))
        case .folder:
            let x = (a.cwd as NSString).lastPathComponent.lowercased()
            let y = (b.cwd as NSString).lastPathComponent.lowercased()
            return x != y ? order(x, y) : names()
        }
    }

    /// The CLI's status order (internal/core statusRank): what needs you first.
    static func statusRank(_ s: Session) -> Int {
        switch s.status {
        case "waiting": 0
        case "error": 1
        case "busy": 2
        case "done": s.unseen ? 3 : 4
        case "starting", "unknown": 5
        case "paused", "finished": 6
        default: 7
        }
    }
}

/// What dragging something in the sidebar onto a group does.
enum SidebarDrop: Equatable {
    case moveSessions([String], to: String)
    case moveGroup(String, before: String?, after: String?)

    static func sessionItem(_ id: String) -> String { "session:" + id }
    static func groupItem(_ id: String) -> String { "group:" + id }

    /// The change a drop onto target asks for: a group's key, or a host's no
    /// group (SidebarLayout.noGroupKey). A dragged session that's marked takes
    /// every marked session with it; on no group, they leave their group. A
    /// group dropped on another goes before it; on its host's "No group" line,
    /// after its host's last group. Anything else is nothing. A group moves
    /// only among its own host's.
    static func action(items: [String], onto target: String, groups: [SessionGroup], marked: Set<String> = []) -> SidebarDrop? {
        guard let item = items.first else { return nil }
        if item.hasPrefix("session:") {
            var ids = items.compactMap { $0.hasPrefix("session:") ? String($0.dropFirst(8)) : nil }
            if ids.contains(where: marked.contains) { ids = Array(marked).sorted() }
            return ids.isEmpty ? nil : .moveSessions(ids, to: target)
        }
        guard item.hasPrefix("group:") else { return nil }
        let key = String(item.dropFirst(6))
        let ordered = SidebarLayout.orderedGroups(groups)
        guard key != target, let dragged = ordered.first(where: { $0.key == key }) else { return nil }
        if let host = SidebarLayout.noGroupHost(target) {
            guard host == dragged.host, let last = ordered.last(where: { $0.host == host }), last.key != key else { return nil }
            return .moveGroup(key, before: nil, after: last.key)
        }
        guard let onto = ordered.first(where: { $0.key == target }), onto.host == dragged.host else { return nil }
        return .moveGroup(key, before: target, after: nil)
    }
}
