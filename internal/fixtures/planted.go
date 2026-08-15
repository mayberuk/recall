package fixtures

import "path/filepath"

// Planted tokens. Each appears exactly once in the corpus, so a count of hits is
// an assertion and not an approximation. Plimwarden is the exception by design:
// it sits on one record uuid that two files carry, so a scan that skips uuid
// dedup reports two where the contract says one.
const (
	NeedleConversation = "quixotrope"
	NeedleThinking     = "flimberdash"
	NeedleInvocation   = "grimbleflax"
	NeedleResult       = "wobbleknuth"
	NeedleSubagent     = "snorplewick"
	NeedleRemoteless   = "thrumbaloo"
	NeedleDuplicated   = "plimwarden"
)

// Record uuids the manifest refers to.
const (
	uuidNeedleAssistant = "aaaaaaaa-0000-4000-8000-000000000002"
	uuidNeedleToolUse   = "aaaaaaaa-0000-4000-8000-000000000003"
	uuidSubagentText    = "bbbbbbbb-0000-4000-8000-000000000002"
	uuidRemotelessText  = "ffffffff-0000-4000-8000-000000000002"
	uuidDupAssistant    = "cccccccc-0000-4000-8000-000000000002"
	uuidDupTyped        = "cccccccc-0000-4000-8000-000000000001"
	uuidHugeResult      = "77777777-0000-4000-8000-000000000003"
)

func manifest(scratch string) Manifest {
	at := func(rel string) string { return filepath.Join(scratch, rel) }
	return Manifest{
		Needles: []Needle{
			{NeedleConversation, SessNeedle, uuidNeedleAssistant, TierConversation, []string{FileNeedle}},
			{NeedleThinking, SessNeedle, uuidNeedleAssistant, TierConversation, []string{FileNeedle}},
			{NeedleInvocation, SessNeedle, uuidNeedleToolUse, TierInvocation, []string{FileNeedle}},
			{NeedleResult, SessHugeResult, uuidHugeResult, TierResult, []string{FileHugeResult}},
			{NeedleSubagent, SessNeedle, uuidSubagentText, TierConversation, []string{FileSubagent}},
			{NeedleRemoteless, SessRemoteless, uuidRemotelessText, TierConversation, []string{FileRemoteless}},
			{NeedleDuplicated, SessDup, uuidDupAssistant, TierConversation, []string{FileDupA, FileDupB}},
		},
		DupUUIDs: []DupUUID{
			{uuidDupTyped, SessDup, []string{FileDupA, FileDupB}},
			{uuidDupAssistant, SessDup, []string{FileDupA, FileDupB}},
		},
		CWDShapes: []CWDShape{
			{"normal", at(ScratchNormal), SessNeedle, RepoRemote, OriginURL, at(ScratchNormal)},
			{"subdir", at(ScratchAndroid), SessSubdir, RepoRemote, OriginURL, at(ScratchNormal)},
			{"orphan-worktree", at(ScratchOrphan), SessOrphan, RepoRemote, OriginURL, at(ScratchNormal)},
			{"remoteless", at(ScratchRemoteless), SessRemoteless, RepoNoRemote, "", at(ScratchRemoteless)},
			{"relocated", at(ScratchGone), SessRelocated, RepoNone, "", ""},
		},
		Sessions: []string{
			SessHugeResult, SessSubdir, SessUnknownType, SessNeedle, SessRelocated,
			SessEmptyThinking, SessNoPromptSource, SessDup, SessSkew, SessOrphan,
			SessRemoteless, SessMultiFirst, SessMultiSecond,
		},
		SessionFiles:     13,
		SubagentDirs:     []string{"-scratch-normal/" + SessNeedle + "/subagents"},
		MultiSessionFile: FileMultiSession,
		MultiSessionIDs:  []string{SessMultiFirst, SessMultiSecond},

		TypedTurns:       13,
		TypedTurnRecords: 14,
		CommandArgTurns:  1,
		HumanTurns:       14,

		UnknownTypes:    map[string]int{"quantum-checkpoint": 2, "holo-summary": 1},
		HugeResultBytes: 22053,

		SkewFile:      FileSkew,
		SkewContentTS: "2026-06-07T09:00:00Z",
		SkewMTime:     "2026-08-01T09:00:00Z",
		SkewDays:      55,
	}
}
