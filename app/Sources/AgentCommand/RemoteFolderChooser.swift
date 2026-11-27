import Foundation
import Observation

/// The group a new session goes in: one that's there (nil: no group), or one
/// to make with this name.
nonisolated enum NewSessionGroup: Equatable {
    case existing(SessionGroup?)
    case new(String)
}

/// A line of ⇧⌘N's group list.
nonisolated enum NewSessionGroupRow: Equatable, Identifiable {
    case group(SessionGroup)
    /// No group: it isn't a group.
    case none
    /// Make a group with the name typed: "" until there is one.
    case new(String)

    var id: String {
        switch self {
        case .group(let group): group.key
        case .none: "none"
        case .new: "new"
        }
    }

    var title: String {
        switch self {
        case .group(let group): group.name
        case .none: "No group"
        case .new(let name): name.isEmpty ? "New group…" : "New group “\(name)”"
        }
    }
}

/// ⌘⇧N's choices before the folder: where the session runs (this Mac or a
/// Remote host), what it runs, and its group. A host's harnesses and default come from it.
@Observable final class NewSessionPicker {
    /// The known hosts' names: where a session can run, after this Mac.
    let hosts: [String]
    /// nil: this Mac.
    private(set) var host: String?
    private(set) var choice = HarnessChoice()
    private(set) var loading = false
    /// Why the host's harnesses couldn't be read.
    private(set) var error: String?
    @ObservationIgnored let client: RemoteHostsClient
    @ObservationIgnored private var generation = 0
    /// Every host's live groups. None: there's no group line.
    let groups: [SessionGroup]
    /// The group it goes in.
    private(set) var group = NewSessionGroup.existing(nil)
    /// Whether the group list (⌘G) is open, instead of the harnesses.
    private(set) var choosingGroup = false
    /// What's typed while the group list is open: it narrows the list, and names a new group.
    var groupSearch = "" {
        didSet { groupCursor = 0 }
    }
    private(set) var groupCursor = 0

    init(hosts: [String], host: String? = nil, client: RemoteHostsClient = RemoteHostsClient(),
         groups: [SessionGroup] = [], group: SessionGroup? = nil) {
        self.hosts = hosts
        self.client = client
        self.groups = groups
        setHost(host.flatMap { hosts.contains($0) ? $0 : nil })
        if let group, group.host == (self.host ?? "") { self.group = .existing(group) }
    }

    /// What opens and closes the group list.
    static let groupKey = KeyCombo("cmd+g")

    /// The chosen host's groups, in order.
    var hostGroups: [SessionGroup] { SidebarLayout.orderedGroups(groups).filter { $0.host == (host ?? "") } }

    /// The group line's text.
    var groupTitle: String {
        switch group {
        case .existing(let group?): group.name
        case .existing(nil): "No group"
        case .new(let name): name + " (new)"
        }
    }

    /// The group's colour, if it has one yet.
    var groupColor: String? {
        if case .existing(let group?) = group { return group.color }
        return nil
    }

    /// The group list: the host's groups that match what's typed, no group
    /// (while nothing's typed), then a new one.
    var groupRows: [NewSessionGroupRow] {
        let typed = groupSearch.trimmingCharacters(in: .whitespaces)
        let shown = hostGroups.filter { typed.isEmpty || $0.name.localizedCaseInsensitiveContains(typed) }
        let exact = shown.contains { $0.name.caseInsensitiveCompare(typed) == .orderedSame }
        return shown.map(NewSessionGroupRow.group) + (typed.isEmpty ? [.none] : []) + (exact ? [] : [.new(typed)])
    }

    func toggleGroups() {
        choosingGroup.toggle()
        groupSearch = ""
        if choosingGroup, case .existing(let current) = group {
            let id = current?.key ?? NewSessionGroupRow.none.id
            groupCursor = groupRows.firstIndex { $0.id == id } ?? 0
        }
    }

    func moveGroupCursor(_ step: Int) {
        let count = groupRows.count
        guard count > 0 else { return }
        groupCursor = max(0, min(count - 1, groupCursor + step))
    }

    /// Takes a group row: false when it's a new group with no name yet.
    @discardableResult func pickGroup(_ row: NewSessionGroupRow) -> Bool {
        switch row {
        case .group(let picked): group = .existing(picked)
        case .none: group = .existing(nil)
        case .new(let name):
            guard !name.isEmpty else { return false }
            group = .new(name)
        }
        choosingGroup = false
        groupSearch = ""
        return true
    }

    /// Takes the row the cursor is on.
    @discardableResult func pickGroupAtCursor() -> Bool {
        let rows = groupRows
        return rows.indices.contains(groupCursor) && pickGroup(rows[groupCursor])
    }

    /// Where, in order: nil (this Mac), then each host.
    var places: [String?] { [nil] + hosts.map { $0 } }

    static func title(_ host: String?) -> String { host ?? "This Mac" }

    /// Only this Mac's sessions can be what ⌘N starts.
    var canMakeDefault: Bool { host == nil }

    func setHost(_ name: String?) {
        host = name
        error = nil
        // A group is on one host: on another, it's no group. A new group is
        // made where the session runs.
        if case .existing(let current?) = group, current.host != (name ?? "") { group = .existing(nil) }
        generation += 1
        guard let name else {
            loading = false
            choice = HarnessChoice()
            return
        }
        choice = HarnessChoice([])
        loading = true
        let mine = generation
        client.harnesses(host: name) { [weak self] result in
            guard let self, mine == self.generation else { return }
            self.loading = false
            switch result {
            case .success(let found):
                self.choice = HarnessChoice(found.known)
                if let preferred = found.defaultHarness { self.choice.select(preferred) }
                if found.known.isEmpty { self.error = "\(name) has nothing this app can start." }
            case .failure(let failure):
                self.error = errorText(failure)
            }
        }
    }

    /// The next or previous place, going round.
    func movePlace(_ step: Int) {
        let list = places
        let index = list.firstIndex { $0 == host } ?? 0
        setHost(list[(index + step + list.count) % list.count])
    }

    func move(_ step: Int) { choice.move(step) }
    func select(_ harness: Harness) { choice.select(harness) }
    func setSearch(_ text: String) -> Harness? { choice.setSearch(text) }

    /// ⌘G opens or closes the group list. tab and shift-tab change where; so do
    /// left and right with nothing typed.
    func handle(_ combo: KeyCombo) -> Bool {
        if combo == Self.groupKey, !groups.isEmpty {
            toggleGroups()
            return true
        }
        guard !hosts.isEmpty, !choosingGroup else { return false }
        if combo == KeyCombo("tab") { movePlace(1); return true }
        if combo == KeyCombo("shift+tab") { movePlace(-1); return true }
        guard choice.search.isEmpty else { return false }
        switch Keymap.shared.action(for: combo, among: [.left, .right]) {
        case .left: movePlace(-1)
        case .right: movePlace(1)
        default: return false
        }
        return true
    }
}

/// The folder for a new session on a Remote host: the host's recent folders,
/// or its folders to browse, with New Folder. What's typed narrows the list.
@Observable final class RemoteFolderChooser {
    enum Mode { case recent, browse }

    enum Row: Identifiable, Equatable {
        /// Start in the folder that's listed.
        case here(String)
        case up(String)
        case folder(RemoteFolder)
        /// A prompt file, when the chooser picks one.
        case file(RemoteFolder)
        /// Browse from the host's home.
        case browse
        case newFolder

        var id: String {
            switch self {
            case .here(let path): "here:" + path
            case .up(let path): "up:" + path
            case .folder(let folder): "folder:" + folder.path
            case .file(let file): "file:" + file.path
            case .browse: "browse"
            case .newFolder: "new"
            }
        }
    }

    /// The host whose folders these are; nil is this Mac's.
    let host: String?
    /// It picks a file in a folder, rather than a folder: a Markdown or text one,
    /// or one with fileExts.
    let picksFile: Bool
    let fileExts: [String]?
    @ObservationIgnored let client: RemoteHostsClient
    private(set) var mode = Mode.recent
    private(set) var recent: [RemoteFolder] = []
    private(set) var listing: RemoteFolderList?
    private(set) var loading = false
    /// What went wrong on the host: a folder that's gone, no permission.
    private(set) var error: String?
    /// Typing a new folder's name: the search line holds it.
    private(set) var naming = false
    private(set) var showsHidden = false
    private(set) var search = ""
    private(set) var selected = 0
    @ObservationIgnored private var generation = 0
    /// Called with the folder chosen, on the host.
    @ObservationIgnored var choose: (String) -> Void = { _ in }

    init(host: String?, picksFile: Bool = false, fileExts: [String]? = nil, client: RemoteHostsClient = RemoteHostsClient()) {
        self.host = host
        self.picksFile = picksFile
        self.fileExts = fileExts
        self.client = client
    }

    /// Where the folders are: a host, or this Mac.
    var hostName: String { host ?? "this Mac" }

    /// Recent folders first; with none, the home folder.
    func start() {
        loading = true
        let mine = next()
        client.recentFolders(host: host) { [weak self] result in
            guard let self, mine == self.generation else { return }
            self.loading = false
            switch result {
            case .success(let list) where !list.folders.isEmpty:
                self.recent = list.folders
                self.mode = .recent
                self.reset()
            case .success:
                self.browse("")
            case .failure(let failure):
                self.error = errorText(failure)
                self.browse("")
            }
        }
    }

    private func next() -> Int {
        generation += 1
        return generation
    }

    private func reset() {
        search = ""
        selected = 0
    }

    /// Lists a folder: absolute, or from the host's home ("" is home).
    func browse(_ path: String) {
        loading = true
        let mine = next()
        client.listFolders(host: host, path: path, hidden: showsHidden, files: picksFile, exts: fileExts) { [weak self] result in
            guard let self, mine == self.generation else { return }
            self.loading = false
            switch result {
            case .success(let list):
                self.listing = list
                self.mode = .browse
                self.error = nil
                self.reset()
            case .failure(let failure):
                // What was listed stays, with why this one couldn't be.
                self.error = errorText(failure)
            }
        }
    }

    /// The folder that's listed, as "~/src" when it's in the host's home.
    var place: String {
        guard mode == .browse, let listing else { return "Recent folders on \(hostName)" }
        return Self.abbreviate(listing.path, home: listing.home)
    }

    static func abbreviate(_ path: String, home: String) -> String {
        guard !home.isEmpty, path == home || path.hasPrefix(home + "/") else { return path }
        return "~" + path.dropFirst(home.count)
    }

    var rows: [Row] {
        guard !naming else { return [] }
        let query = search.trimmingCharacters(in: .whitespaces)
        func matching(_ folders: [RemoteFolder]) -> [Row] {
            folders.filter { query.isEmpty || $0.name.localizedCaseInsensitiveContains(query) || $0.path.localizedCaseInsensitiveContains(query) }
                .map { .folder($0) }
        }
        switch mode {
        case .recent:
            return matching(recent) + [.browse]
        case .browse:
            guard let listing else { return [] }
            var list: [Row] = picksFile ? [] : [.here(listing.path)]
            if !listing.parent.isEmpty, listing.parent != listing.path { list.append(.up(listing.parent)) }
            let files = listing.files.filter { query.isEmpty || $0.name.localizedCaseInsensitiveContains(query) }.map { Row.file($0) }
            return list + matching(listing.folders) + files + [.newFolder]
        }
    }

    var chosen: Row? {
        let list = rows
        return list.indices.contains(selected) ? list[selected] : nil
    }

    func setSearch(_ text: String) {
        search = text
        if !naming { selected = 0 }
    }

    func move(_ step: Int) {
        let count = rows.count
        if count > 0 { selected = max(0, min(count - 1, selected + step)) }
    }

    func select(_ row: Row) {
        if let i = rows.firstIndex(of: row) { selected = i }
    }

    /// Return: a recent folder or "start here" is chosen, a folder opens, and
    /// New Folder asks its name, then makes it.
    func submit() {
        if naming { return makeFolder() }
        guard let row = chosen else { return }
        switch row {
        case .here(let path): choose(path)
        case .up(let path): browse(path)
        case .folder(let folder): mode == .recent && !picksFile ? choose(folder.path) : browse(folder.path)
        case .file(let file): choose(file.path)
        case .browse: browse("")
        case .newFolder: startNaming()
        }
    }

    func startNaming() {
        guard mode == .browse, listing != nil else { return }
        naming = true
        search = ""
        error = nil
    }

    /// Escape: stops naming a folder. False when there's nothing to stop, so the chooser closes.
    func back() -> Bool {
        guard naming else { return false }
        naming = false
        reset()
        error = nil
        return true
    }

    /// Right opens the folder at the cursor, and left goes up. Only with
    /// nothing typed, so they still move along what's typed.
    func open() {
        if case .folder(let folder) = chosen { browse(folder.path) }
    }

    func up() {
        if mode == .browse, let parent = listing?.parent, !parent.isEmpty, parent != listing?.path { browse(parent) }
    }

    /// Tab: recent folders, or browsing.
    func toggleMode() {
        if mode == .browse, !recent.isEmpty {
            mode = .recent
            naming = false
            reset()
        } else if mode == .recent {
            browse(listing?.path ?? "")
        }
    }

    func toggleHidden() {
        showsHidden.toggle()
        if mode == .browse { browse(listing?.path ?? "") }
    }

    /// Makes the folder named in the search line, in the listed one, then opens it.
    func makeFolder() {
        let name = search.trimmingCharacters(in: .whitespaces)
        guard let listing else { return }
        if let problem = Self.nameProblem(name) {
            error = problem
            return
        }
        loading = true
        let path = (listing.path as NSString).appendingPathComponent(name)
        client.makeFolder(host: host, path: path) { [weak self] result in
            guard let self else { return }
            self.loading = false
            switch result {
            case .success(let folder):
                self.naming = false
                self.browse(folder.path.isEmpty ? path : folder.path)
            case .failure(let failure):
                self.error = errorText(failure)
            }
        }
    }

    /// The session couldn't start in the folder chosen: say why, and stay open.
    func fail(_ message: String) {
        loading = false
        error = message
    }

    static func nameProblem(_ name: String) -> String? {
        if name.isEmpty { return "Type the new folder's name." }
        if name.contains("/") { return "A folder's name can't have / in it." }
        if name == "." || name == ".." { return "Choose another name." }
        return nil
    }

    func handle(_ combo: KeyCombo) -> Bool {
        if combo == KeyCombo("tab") { toggleMode(); return true }
        if combo == KeyCombo("cmd+n") { startNaming(); return true }
        if combo == KeyCombo("shift+cmd+.") { toggleHidden(); return true }
        guard search.isEmpty, !naming else { return false }
        switch Keymap.shared.action(for: combo, among: [.left, .right]) {
        case .left: up()
        case .right: open()
        default: return false
        }
        return true
    }

    /// The keys, under the list.
    static let hint = "return choose or open  ←→ up and into  tab recent or browse  ⌘N new folder  ⇧⌘. hidden"
}
