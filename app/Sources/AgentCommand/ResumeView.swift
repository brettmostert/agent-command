import SwiftUI

/// Picks a session to resume, here or on a Remote host, or takes an agent's
/// session id. Where to list from is the sidebar's filter (All, Local, Remote,
/// a host); Tab moves through it.
struct ResumeView: View {
    let store: SessionStore
    let close: () -> Void
    let choose: (ResumeTarget) -> Void

    @State private var choice = ResumeChoice(filter: ResumeView.savedFilter)
    @State private var loading = true
    @State private var problem: String?
    @State private var unreached: [String] = []

    private var theme: AppTheme { .shared }

    /// Where the window's filter is kept: its own, apart from the sidebar's.
    static let filterKey = "resumeFilter"
    static var savedFilter: SidebarFilter { SidebarFilter(rawValue: UserDefaults.standard.string(forKey: filterKey) ?? "") }

    var body: some View {
        let list = choice.rows
        PopupBox(
            title: "Resume a session",
            placeholder: "Search by name, id or folder, or paste a session id",
            search: Binding(get: { choice.search }, set: { choice.setSearch($0) }),
            close: close,
            onMove: { choice.move($0) },
            onSubmit: {
                if let row = choice.chosen { choose(row.target) }
            },
            onKey: { combo in
                guard !choice.choices.isEmpty else { return false }
                if combo == KeyCombo("tab") { moveFilter(1); return true }
                if combo == KeyCombo("shift+tab") { moveFilter(-1); return true }
                return false
            }
        ) {
            VStack(alignment: .leading, spacing: 0) {
                if !choice.choices.isEmpty { whereLine }
                rows(list)
            }
        }
        .onAppear {
            choice.hosts = store.remoteHosts.map(\.name)
            store.loadResumable { found, missed, error in
                choice.sessions = found
                unreached = missed
                problem = error
                loading = false
            }
        }
    }

    private func moveFilter(_ delta: Int) {
        choice.moveFilter(delta)
        UserDefaults.standard.set(choice.filter.rawValue, forKey: Self.filterKey)
    }

    /// All, Local, Remote and each host, as the sidebar offers them.
    private var whereLine: some View {
        HStack(spacing: 4) {
            Text("Where").foregroundStyle(theme.dim)
            ForEach(choice.choices) { place in
                let isSelected = place == choice.shownFilter
                Text(place.title)
                    .fontWeight(isSelected ? .bold : .regular)
                    .foregroundStyle(isSelected ? theme.bg : theme.fg)
                    .padding(.horizontal, 6)
                    .background(isSelected ? theme.yellow : Color.clear)
                    .contentShape(Rectangle())
                    .onTapGesture {
                        choice.filter = place
                        moveFilter(0)
                    }
            }
            Spacer()
            Text("tab").foregroundStyle(theme.dim)
        }
        .padding(.horizontal, 24)
        .padding(.bottom, 12)
    }

    private func rows(_ list: [ResumeChoice.Row]) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(messages(list), id: \.self) { message in
                        Text(message)
                            .foregroundStyle(problem == nil ? theme.dim : theme.red)
                            .padding(.horizontal, 24)
                    }
                    ForEach(list) { row in
                        let isSelected = row == choice.chosen
                        Group {
                            switch row {
                            case .session(let session):
                                ResumeRow(session: session, isSelected: isSelected)
                            case .typed(_, let harness, let host):
                                TypedIDRow(harness: harness, host: host, isSelected: isSelected)
                            }
                        }
                        .id(row.id)
                        .contentShape(Rectangle())
                        .onTapGesture { choose(row.target) }
                    }
                }
            }
            .frame(maxHeight: 520)
            .fixedSize(horizontal: false, vertical: true)
            .onChange(of: choice.chosen) {
                if let row = choice.chosen { proxy.scrollTo(row.id) }
            }
        }
    }

    private func messages(_ list: [ResumeChoice.Row]) -> [String] {
        if let problem { return [problem] }
        if loading { return ["Loading…"] }
        var out = unreached.filter { choice.lookIn.contains($0) }.map { "\($0) didn't answer: its sessions aren't listed" }
        if choice.typedID != nil {
            out.append("Whose session is this id? Press return to look it up")
        } else if list.isEmpty {
            out.append("No matching sessions")
        }
        return out
    }
}

/// The agent's mark, in the row's colors.
private struct HarnessMark: View {
    let harness: Harness
    let isSelected: Bool
    private var theme: AppTheme { .shared }

    var body: some View {
        let color = isSelected ? theme.bg : theme.fg
        switch harness.icon {
        case "antigravity": AgentMark(mark: AntigravityMarkShape(), label: harness.title, color: color, busy: false)
        case "claude": AgentMark(mark: ClaudeMarkShape(), label: harness.title, color: color, busy: false)
        default: Text("●").foregroundStyle(color).help(harness.title) // one the app has no mark for
        }
    }
}

/// Looks the typed id up as one agent's, on this Mac or a host.
private struct TypedIDRow: View {
    let harness: Harness
    let host: String
    let isSelected: Bool
    private var theme: AppTheme { .shared }

    var body: some View {
        HStack(spacing: 8) {
            HarnessMark(harness: harness, isSelected: isSelected)
            Text("As \(harness.isAntigravity ? "an" : "a") \(harness.title) session")
                .fontWeight(isSelected ? .bold : .regular)
                .foregroundStyle(isSelected ? theme.bg : theme.fg)
            Spacer(minLength: 12)
            if !host.isEmpty {
                Text("↗ \(host)").foregroundStyle(isSelected ? theme.bg : theme.cyan)
            }
        }
        .padding(.horizontal, 8)
        .frame(height: 19)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }
}

/// A session that can be resumed: its name, where it is and how it stands, and
/// under it its folder, branch and harness.
private struct ResumeRow: View {
    let session: Session
    let isSelected: Bool

    private var theme: AppTheme { .shared }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                HarnessMark(harness: session.harness, isSelected: isSelected)
                    .help(session.harnessTitle)
                Text(session.displayName.isEmpty ? "Untitled" : session.displayName)
                    .fontWeight(isSelected ? .bold : .regular)
                    .foregroundStyle(isSelected ? theme.bg : theme.fg)
                    .lineLimit(1)
                Spacer(minLength: 12)
                if session.isOnHost {
                    Text("↗ \(session.hostName)").foregroundStyle(isSelected ? theme.bg : theme.cyan).lineLimit(1)
                }
                if session.agentLive == true {
                    Text("running").foregroundStyle(isSelected ? theme.bg : theme.yellow)
                } else if !session.id.isEmpty {
                    Text(session.status).foregroundStyle(isSelected ? theme.bg : theme.dim)
                }
                if let updated = session.lastActive {
                    Text(updated, format: .relative(presentation: .named))
                        .foregroundStyle(isSelected ? theme.bg : theme.dim)
                        .lineLimit(1)
                }
            }
            HStack(spacing: 8) {
                Text(detail)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 12)
                Text(session.harnessTitle).lineLimit(1)
            }
            .foregroundStyle(isSelected ? theme.bg : theme.dim)
            // Under the name, past the mark.
            .padding(.leading, 21)
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 3)
        .background(isSelected ? theme.yellow : Color.clear)
        .padding(.horizontal, 16)
    }

    /// A host's folder is as it gave it: this Mac's home means nothing there.
    private var detail: String {
        let folder = session.isOnHost ? session.cwd : (session.cwd as NSString).abbreviatingWithTildeInPath
        guard let branch = session.branch, !branch.isEmpty else { return folder }
        return "\(folder) · \(branch)"
    }
}
