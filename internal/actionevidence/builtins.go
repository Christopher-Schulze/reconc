package actionevidence

import "sort"

func BuiltinPacks() ([]LoadedPack, error) {
	packs := []Pack{soc2Pack(), gdprPack(), hipaaPack(), euAIActPack()}
	loaded := make([]LoadedPack, len(packs))
	for index, pack := range packs {
		identity, err := PackIdentity(pack)
		if err != nil {
			return nil, err
		}
		loaded[index] = LoadedPack{Pack: pack, Identity: identity, Provenance: "builtin"}
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].Pack.PackID < loaded[j].Pack.PackID })
	return loaded, nil
}

func soc2Pack() Pack {
	return newBuiltinPack(
		"soc2-technical-evidence", "SOC 2 Trust Services Criteria",
		Source{
			URL:        "https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022",
			Edition:    "2017 Trust Services Criteria with revised points of focus 2022",
			SourceDate: "2023-09-30", ReviewedAt: "2026-08-12",
			ReuseNotice: "AICPA source referenced by identifier; mappings use original Reconc paraphrases and embed no source quotation.",
		},
		[]Control{
			newControl("soc2.cc6-1", "CC6.1", "Maps enforced action access rules and independently verified approval authority evidence to logical access safeguards.", []FactID{FactApprovalAuthority, FactApprovalReceipts, FactHostCoverage, FactPolicyActionRules, FactPolicyActionTools, FactPolicyLockCurrent}, []string{"Asset inventory, user lifecycle, network controls, and organization-wide access governance remain outside Reconc."}),
			newControl("soc2.cc7-2", "CC7.2", "Maps verified action events and complete call lifecycles to technical monitoring evidence.", []FactID{FactLedgerCallsComplete, FactLedgerEventsComplete, FactLedgerIntegrity, FactLedgerWindowComplete}, []string{"Organization-wide anomaly monitoring and human analysis remain outside Reconc."}),
			newControl("soc2.cc7-3", "CC7.3", "Maps deterministic decisions, result inspection evidence, and terminal outcomes to technical security-event evaluation evidence.", []FactID{FactLedgerCallsComplete, FactLedgerEventsComplete, FactLedgerIntegrity, FactScenarioResults}, []string{"Incident classification, response ownership, and business impact analysis remain outside Reconc."}),
			newControl("soc2.cc8-1", "CC8.1", "Maps immutable policy identity and exact scenario replay to tested action-policy change evidence.", []FactID{FactLedgerPolicyIdentity, FactPolicyLockCurrent, FactScenarioCompleteness, FactScenarioResults}, []string{"Change authorization, deployment governance, and reviewer independence remain outside Reconc."}),
		},
	)
}

func gdprPack() Pack {
	return newBuiltinPack(
		"gdpr-technical-evidence", "GDPR",
		Source{
			URL:        "https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:02016R0679-20160504",
			Edition:    "Consolidated Regulation EU 2016/679",
			SourceDate: "2016-05-04", ReviewedAt: "2026-08-12",
			ReuseNotice: "EUR-Lex legal source referenced by article; mappings are original technical paraphrases.",
		},
		[]Control{
			newControl("gdpr.article-24-1", "Article 24(1)", "Maps current enforced policy, verified operation records, and scenario evidence to technical measures an operator can review.", []FactID{FactLedgerCallsComplete, FactLedgerIntegrity, FactPolicyLockCurrent, FactScenarioCompleteness, FactScenarioResults}, []string{"Controller obligations, processing context, proportionality, and periodic organizational review remain outside Reconc."}),
			newControl("gdpr.article-25-1", "Article 25(1)", "Maps deterministic fail-closed action rules and verified scenario coverage to technical design evidence.", []FactID{FactHostCoverage, FactPolicyActionRules, FactPolicyActionTools, FactPolicyLockCurrent, FactScenarioCompleteness}, []string{"Data-protection principles, processing purposes, necessity, and lifecycle design remain outside Reconc."}),
			newControl("gdpr.article-32-1-d", "Article 32(1)(d)", "Maps repeatable scenario evaluation and integrity-checked operational evidence to technical testing evidence.", []FactID{FactLedgerArchiveContinuity, FactLedgerIntegrity, FactScenarioCompleteness, FactScenarioResults}, []string{"Risk appropriateness, recovery, confidentiality, and organization-wide security measures remain outside Reconc."}),
			newControl("gdpr.article-5-2", "Article 5(2)", "Maps current policy identity and retained technical decision evidence to a bounded accountability record.", []FactID{FactLedgerIntegrity, FactLedgerPolicyIdentity, FactLedgerWindowComplete, FactPolicyLockCurrent}, []string{"Lawfulness, purpose, data lifecycle, controller accountability, and evidence outside the selected window remain outside Reconc."}),
		},
	)
}

func hipaaPack() Pack {
	return newBuiltinPack(
		"hipaa-security-technical-evidence", "HIPAA Security Rule",
		Source{
			URL:        "https://www.ecfr.gov/current/title-45/subtitle-A/subchapter-C/part-164/subpart-C",
			Edition:    "45 CFR Part 164 Subpart C",
			SourceDate: "2026-08-10", ReviewedAt: "2026-08-12",
			ReuseNotice: "US eCFR source referenced by section; mappings are original technical paraphrases.",
		},
		[]Control{
			newControl("hipaa.164-308-a-1-ii-d", "45 CFR 164.308(a)(1)(ii)(D)", "Maps integrity-checked action activity and complete lifecycle evidence to a bounded technical review record.", []FactID{FactLedgerCallsComplete, FactLedgerEventsComplete, FactLedgerIntegrity, FactLedgerWindowComplete}, []string{"Periodic review procedure, workforce responsibility, and systems outside Reconc remain outside this mapping."}),
			newControl("hipaa.164-312-b", "45 CFR 164.312(b)", "Maps the tamper-evident action ledger and retained-history verification to technical activity-record evidence.", []FactID{FactLedgerArchiveContinuity, FactLedgerIntegrity, FactLedgerWindowComplete}, []string{"Coverage is limited to action routes explicitly passing through Reconc."}),
			newControl("hipaa.164-312-c-1", "45 CFR 164.312(c)(1)", "Maps immutable policy identity, record chaining, and current-policy binding to technical integrity evidence.", []FactID{FactLedgerIntegrity, FactLedgerPolicyIdentity, FactPolicyLockCurrent}, []string{"Integrity of systems, data stores, and transmissions outside Reconc remains outside this mapping."}),
			newControl("hipaa.164-312-d", "45 CFR 164.312(d)", "Maps operator-owned authority registries and reverified signed receipts to technical person-or-entity authorization evidence.", []FactID{FactApprovalAuthority, FactApprovalReceipts}, []string{"Identity proofing, workforce authentication, and downstream system authentication remain outside Reconc."}),
			newControl("hipaa.164-316-b", "45 CFR 164.316(b)", "Maps versioned policy identity and explicit retained evidence boundaries to technical documentation evidence.", []FactID{FactLedgerArchiveContinuity, FactLedgerWindowComplete, FactPolicyLockCurrent}, []string{"Required retention duration, policies, procedures, and organizational documentation remain operator responsibilities."}),
		},
	)
}

func euAIActPack() Pack {
	return newBuiltinPack(
		"eu-ai-act-technical-evidence", "EU AI Act",
		Source{
			URL:        "https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX:02024R1689-20260727",
			Edition:    "Consolidated Regulation EU 2024/1689",
			SourceDate: "2026-07-27", ReviewedAt: "2026-08-12",
			ReuseNotice: "EUR-Lex legal source referenced by article; mappings are original technical paraphrases.",
		},
		[]Control{
			newControl("eu-ai-act.article-11-1", "Article 11(1)", "Maps versioned policy, scenarios, and exact operational evidence boundaries to technical documentation inputs.", []FactID{FactLedgerPolicyIdentity, FactPolicyLockCurrent, FactScenarioCompleteness, FactScenarioResults}, []string{"The required technical documentation set, system description, and provider obligations remain outside Reconc."}),
			newControl("eu-ai-act.article-12-1", "Article 12(1)", "Maps tamper-evident event records and lifecycle completeness to technical logging evidence.", []FactID{FactLedgerArchiveContinuity, FactLedgerCallsComplete, FactLedgerEventsComplete, FactLedgerIntegrity, FactLedgerWindowComplete}, []string{"Applicability, logging duration, downstream logs, and deployer access remain outside Reconc."}),
			newControl("eu-ai-act.article-14-1", "Article 14(1)", "Maps explicit approval requirements and reverified authority receipts to bounded human-oversight evidence.", []FactID{FactApprovalAuthority, FactApprovalReceipts, FactPolicyActionRules}, []string{"Oversight competence, authority, user interface, instructions, and intervention effectiveness remain outside Reconc."}),
			newControl("eu-ai-act.article-15-1", "Article 15(1)", "Maps fail-closed policy identity, scenario replay, and complete action lifecycles to technical robustness evidence.", []FactID{FactLedgerCallsComplete, FactLedgerIntegrity, FactPolicyLockCurrent, FactScenarioCompleteness, FactScenarioResults}, []string{"Accuracy levels, resilience testing, cybersecurity program, and model behavior remain outside Reconc."}),
			newControl("eu-ai-act.article-17-1", "Article 17(1)", "Maps versioned policy and repeatable evidence generation to technical quality-system inputs.", []FactID{FactLedgerPolicyIdentity, FactPolicyLockCurrent, FactScenarioCompleteness}, []string{"The provider quality system, governance, resources, post-market monitoring, and regulatory procedures remain outside Reconc."}),
			newControl("eu-ai-act.article-9-1", "Article 9(1)", "Maps deterministic policy evaluation and scenario evidence to bounded technical risk-control inputs.", []FactID{FactHostCoverage, FactPolicyActionRules, FactPolicyLockCurrent, FactScenarioCompleteness, FactScenarioResults}, []string{"Applicability, full lifecycle risk management, residual-risk judgment, testing plans, and human governance remain outside Reconc."}),
		},
	)
}

func newBuiltinPack(id, framework string, source Source, controls []Control) Pack {
	sort.Slice(controls, func(i, j int) bool { return controls[i].ID < controls[j].ID })
	return Pack{
		Schema: PackSchema, FormatVersion: FormatVersion, PackID: id,
		PackVersion: "2026-08-12-1", Framework: framework, ReviewStatus: "reviewed",
		Source: source, Controls: controls,
	}
}

func newControl(id, reference, rationale string, selectors []FactID, gaps []string) Control {
	selectors = append(selectors, FactRepositoryIdentity)
	sort.Slice(selectors, func(i, j int) bool { return selectors[i] < selectors[j] })
	sort.Strings(gaps)
	return Control{
		ID: id, Reference: reference, Rationale: rationale,
		EvidenceSelectors: selectors, KnownGaps: gaps,
	}
}
