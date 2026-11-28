import Foundation
import Testing

@testable import AgentCommand

@Test func harnessesHaveTheirDaemonNames() {
    #expect(Harness.allCases.map(\.kind) == ["claude", "antigravity", "terminal"])
    #expect(Harness.allCases.map(\.title) == ["Claude", "Antigravity", "Terminal"])
    #expect(Harness.allCases.map(\.id) == ["kind:claude", "kind:antigravity", "kind:terminal"])
    #expect(Harness.terminal.panelPrompt == "Open Terminal")
    #expect(Harness.antigravity.panelPrompt == "Start Session" && Harness.claude.panelPrompt == "Start Session")
    #expect(Harness.resolve(id: "", kind: "") == .claude && Harness.resolve(id: "", kind: "antigravity") == .antigravity)
    #expect(Harness.resolve(id: "", kind: "terminal") == .terminal && Harness(id: "gone") == nil)
    #expect(Harness.resolve(id: "gone", kind: "antigravity").name == "Antigravity (removed harness)")
    #expect(Harness.agents == [.claude, .antigravity])
    #expect(Harness.claude.productName == "Claude Code" && Harness.antigravity.productName == "Antigravity")
    #expect(Harness.claude.conversationTitle == "Claude session")
    #expect(Harness.antigravity.conversationTitle == "Antigravity conversation")
}

private func resumable(_ agentID: String, _ name: String, id: String = "", host: String? = nil, harness: String? = nil,
                       cwd: String = "/w", running: Bool = false, capabilities: [String]? = nil) throws -> Session {
    var fields: [String: Any] = ["id": id, "claude_session_id": agentID, "cwd": cwd, "name": name,
                                 "last_event_at": "2026-11-07T10:00:00Z", "agent_live": running]
    if let harness { fields["harness_kind"] = harness }
    if let host { fields["host"] = host }
    if let capabilities { fields["capabilities"] = capabilities }
    return try JSONDecoder().decode(Session.self, from: JSONSerialization.data(withJSONObject: fields))
}

@Test func theResumeWindowListsBothAgents() throws {
    let claude = try resumable("aaaa1111", "Fix the parser", harness: "claude", running: true)
    let gravity = try resumable("bbbb2222", "Plan the release", harness: "antigravity")
    var choice = ResumeChoice([claude, gravity])
    #expect(choice.rows == [.session(claude), .session(gravity)])
    #expect(choice.chosen?.target == ResumeTarget(agentID: "aaaa1111", harnessKind: "claude", harnessName: "Claude", isRunning: true))
    choice.move(1)
    #expect(choice.chosen?.target == ResumeTarget(agentID: "bbbb2222", harnessKind: "antigravity", harnessName: "Antigravity"),
            "resumed as Antigravity's")
    choice.move(1)
    #expect(choice.chosen == .session(claude), "it wraps around")
    choice.select(.session(gravity))
    #expect(choice.chosen == .session(gravity))
    #expect(Set(choice.rows.map(\.id)).count == 2)
    #expect(choice.choices.isEmpty, "no host: nothing to choose from")

    choice.setSearch("release")
    #expect(choice.rows == [.session(gravity)] && choice.typedID == nil)
    choice.setSearch("Antigravity")
    #expect(choice.rows.isEmpty, "the agent's name isn't searched")
}

// A search matches the name, then the id, then the folder; within each, the
// daemon's order (most recent first) stays.
@Test func aSearchRanksNameThenIDThenFolder() throws {
    let inFolder = try resumable("1111aaaa", "one", cwd: "/w/api")
    let byID = try resumable("api0beef", "two")
    let byName = try resumable("2222bbbb", "api work")
    let alsoName = try resumable("3333cccc", "the api")
    var choice = ResumeChoice([inFolder, byID, byName, alsoName])
    choice.setSearch("API")
    #expect(choice.rows == [.session(byName), .session(alsoName), .session(byID), .session(inFolder)])
}

// Where to list from is the sidebar's filter: All, Local, Remote and each host.
// A stopped acmd session resumes as itself; an archived one, or one only the
// agent has, by its agent id, on its host.
@Test func theResumeWindowFiltersByWhere() throws {
    let here = try resumable("aaaa1111", "here")
    let studio = try resumable("bbbb2222", "on studio", id: "r7", host: "studio", harness: "claude", capabilities: ["resume"])
    let mini = try resumable("cccc3333", "on mini", id: "m1", host: "mini", capabilities: ["purge"])
    var choice = ResumeChoice([here, studio, mini], hosts: ["studio"])
    #expect(choice.places == ["studio", "mini"], "known hosts, then hosts with sessions")
    #expect(choice.choices == [.all, .local, .remote, .host("studio"), .host("mini")])
    #expect(choice.rows.count == 3)
    choice.moveFilter(1)
    #expect(choice.shownFilter == .local && choice.rows == [.session(here)])
    choice.moveFilter(1)
    #expect(choice.rows == [.session(studio), .session(mini)])
    choice.moveFilter(1)
    #expect(choice.rows == [.session(studio)])
    #expect(choice.chosen?.target == ResumeTarget(sessionID: "r7", agentID: "bbbb2222", host: "studio", harnessName: "Claude", byID: true))
    choice.moveFilter(1)
    #expect(choice.chosen?.target.byID == false, "archived: by its agent id")
    choice.moveFilter(1)
    #expect(choice.shownFilter == .all, "round the end")
    choice.moveFilter(-1)
    #expect(choice.shownFilter == .host("mini"))
    #expect(ResumeChoice([here], filter: .host("gone")).shownFilter == .all, "a host that's gone lists everything")
}

@Test func aTypedIDAsksWhoseItIs() throws {
    var choice = ResumeChoice([try resumable("aaaa1111", "Fix the parser")])
    choice.setSearch("aaaa")
    #expect(choice.typedID == nil, "it's listed")
    choice.setSearch(" 0123ABCD-ef ")
    #expect(choice.typedID == "0123abcd-ef")
    #expect(choice.rows == [.typed("0123abcd-ef", .claude, host: ""), .typed("0123abcd-ef", .antigravity, host: "")])
    #expect(choice.chosen?.target == ResumeTarget(agentID: "0123abcd-ef", harnessKind: "claude", harnessName: "Claude"), "Claude's first")
    choice.move(1)
    #expect(choice.chosen?.target == ResumeTarget(agentID: "0123abcd-ef", harnessKind: "antigravity", harnessName: "Antigravity"))
    #expect(choice.rows.map(\.id) == ["typed::kind:claude:0123abcd-ef", "typed::kind:antigravity:0123abcd-ef"])
    choice.setSearch("0123abcd-ee")
    #expect(choice.chosen == .typed("0123abcd-ee", .claude, host: ""), "a new search starts at the top")
    choice.setSearch("0123")
    #expect(choice.typedID == nil && choice.rows.isEmpty && choice.chosen == nil, "too short for an id")
    choice.move(1)
    #expect(choice.chosen == nil)
}

// A typed id is looked up on each machine the filter takes in: on a host, by
// kind, as the host has its own harnesses.
@Test func aTypedIDIsLookedUpWhereTheFilterSays() throws {
    var choice = ResumeChoice([], hosts: ["studio"], filter: .host("studio"))
    choice.setSearch("0123abcd")
    #expect(choice.rows.map(\.id) == ["typed:studio:kind:claude:0123abcd", "typed:studio:kind:antigravity:0123abcd"])
    #expect(choice.chosen?.target == ResumeTarget(agentID: "0123abcd", host: "studio", harnessKind: "claude", harnessName: "Claude"))
    choice.filter = .all
    #expect(choice.lookIn == ["", "studio"] && choice.rows.count == 4)
    choice.filter = .remote
    #expect(choice.lookIn == ["studio"])
}

@Test func thePickerMovesAndPicks() {
    var choice = HarnessChoice()
    #expect(choice.matches == [.claude, .antigravity, .terminal])
    #expect(choice.chosen == .claude, "the default comes first")
    choice.move(1)
    #expect(choice.chosen == .antigravity)
    choice.move(1)
    choice.move(1)
    #expect(choice.chosen == .claude, "it wraps around")
    choice.move(-1)
    #expect(choice.chosen == .terminal)
    choice.select(.antigravity)
    #expect(choice.chosen == .antigravity, "the mouse moves it too")
    #expect(choice.number(of: .claude) == 1 && choice.number(of: .terminal) == 3)
}

@Test func typingNarrowsThePicker() {
    var choice = HarnessChoice()
    choice.move(2)
    #expect(choice.setSearch("ANTI") == nil)
    #expect(choice.matches == [.antigravity] && choice.chosen == .antigravity, "the selection starts over")
    #expect(choice.number(of: .antigravity) == 2, "rows keep their numbers")
    #expect(choice.setSearch("zz") == nil)
    #expect(choice.matches.isEmpty && choice.chosen == nil)
    choice.move(1)
    #expect(choice.chosen == nil, "nothing to move through")
    choice.select(.claude)
    #expect(choice.chosen == nil)
    #expect(choice.setSearch("") == nil && choice.matches.count == 3)
}

@Test func aNumberPicksStraightAway() {
    var choice = HarnessChoice()
    #expect(choice.setSearch("1") == .claude)
    #expect(choice.setSearch("2") == .antigravity)
    #expect(choice.setSearch("3") == .terminal)
    #expect(choice.search.isEmpty, "the number isn't kept as a search")
    #expect(choice.setSearch("4") == nil && choice.matches.isEmpty)
    #expect(HarnessChoice([.terminal]).matches == [.terminal])
}
