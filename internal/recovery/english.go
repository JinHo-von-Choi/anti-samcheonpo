package recovery

import "github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"

// english holds the English text of every fixed prescription sentence. The
// ledger keeps the Korean text, so a stored attempt reads the same whichever
// language it was delivered in.
var english = map[string]string{
	HandoffAction: "Do not repeat the earlier prescription. Bundle the observed failures, attempts and unconfirmed conditions and hand them to the user.",
	HandoffStop:   "Start the next strategy only with new information or an explicit choice from the user.",

	"같은 코드 수정을 멈추고 관측된 권한·명령·서비스 장애와 필요한 외부 조치를 사용자에게 요약한다. 권한·설정·모델을 임의로 바꾸지 않는다.":  "Stop the same code edits and summarize the observed permission, command or service failure and the external fix needed for the user. Leave permissions, settings and the model as they are.",
	"외부 조건 변경이 확인되기 전에는 같은 실행을 반복하지 않는다.":                                            "Do not repeat the same run until a change in the external condition is confirmed.",
	"이미 통과한 검사의 명령·입력·환경·목표 개정을 확인한다. 유효한 근거가 있으면 재사용하고, 바뀐 범위 또는 아직 미확인인 조건만 검사한다.": "Check the command, input, environment and goal revision of checks that already passed. Reuse valid evidence, and check only what changed or is still unconfirmed.",
	"수락된 완료 조건에 충분한 근거가 생기면 검사를 끝낸다. 수동 조건은 별도로 남긴다.":                                "End checking once the accepted completion criteria have enough evidence. Leave manual criteria separate.",
	"같은 오류와 실패한 접근을 묶고 새로운 근거가 있는 가설 하나만 좁은 검사로 확인한다. 근거 없이 파일과 명령을 바꾸며 재시도하지 않는다.":  "Group the same error with the failed approaches and test one hypothesis with new evidence using a narrow check. Do not retry by changing files and commands without evidence.",
	"같은 처방이 실패하거나 새 근거가 없으면 인수인계하고 수정 반복을 멈춘다.":                                      "If the same prescription fails or there is no new evidence, hand off and stop repeating edits.",
	"최신 사용자 요청과 현재 변경의 관계를 확인한다. 범위 확대가 필요한 경우 변경 전 선택지를 사용자에게 설명한다.":                "Check how the current changes relate to the latest user request. If the scope has to grow, explain the options to the user before changing anything.",
	"명시적 방향 변경은 반영하되 승인되지 않은 범위 확대는 진행하지 않는다.":                                       "Apply explicit changes of direction, and do not widen the scope without approval.",
	"실패한 접근과 미확인 가정을 요약하고 작업 분할·추가 정보 요청·전략 변경 중 근거 있는 선택지를 제시한다. 모델을 자동 교체하지 않는다.":  "Summarize the failed approaches and unconfirmed assumptions, and offer evidence-based options among splitting the work, asking for more information or changing strategy. Do not switch models automatically.",
	"새 정보나 사용자 선택 없이 같은 전략을 반복하지 않는다.":                                               "Do not repeat the same strategy without new information or a choice from the user.",
	"최신 목표·검증 근거·이미 실패한 접근을 인수인계 자료로 묶는다. 과거의 통과를 새 환경의 통과로 취급하지 않는다.":               "Bundle the latest goal, verification evidence and already failed approaches as handoff material. Do not treat a past pass as a pass in a new environment.",
	"실패한 접근을 다시 시작하기 전에 달라진 조건을 확인한다.":                                               "Check what has changed before restarting a failed approach.",
	"남아 있는 프로세스가 이 세션이 띄운 것인지 확인한다. 더 쓰지 않을 프로세스만 정리하고, 남겨 둘 프로세스는 이유를 사용자에게 알린다.":   "Check whether the remaining processes were started by this session. Clean up only those no longer needed, and tell the user why any others are kept.",
	"이 세션이 띄웠다는 근거가 없는 프로세스는 종료하지 않는다.":                                              "Leave running any process with no evidence that this session started it.",
	"후보 문장을 지우거나 대상 자체를 설명하는 문장으로 고친다. 지워도 독자가 얻는 내용이 그대로인지 확인한다.":                   "Delete the candidate sentences or rewrite them to describe the subject itself. Confirm the reader still gets the same content.",
	"독자에게 필요한 내용을 지워야 한다면 후보를 그대로 두고 사용자에게 판단을 맡긴다.":                                 "If deleting would remove something the reader needs, keep the candidate and leave the decision to the user.",
	"명시적 예산과 지금까지 관측한 사용량·미확인 비용을 요약한다. 추가 실행이 한도를 넘으면 사용자에게 선택을 요청한다.":              "Summarize the explicit budget, the usage observed so far and any unconfirmed cost. If further runs would exceed the limit, ask the user to choose.",
	"예산 확대나 새 목표 없이 자동 계속하기를 반복하지 않는다.":                                              "Do not keep auto-continuing without a larger budget or a new goal.",
	"관측 사실과 미확인 원인을 분리하고 가장 작은 확인 한 번으로 다음 행동을 정한다.":                                 "Separate observed facts from unconfirmed causes and decide the next action with one smallest check.",
	"확인 뒤에도 원인이 불명확하면 추측성 수정을 이어가지 않는다.":                                             "If the cause is still unclear after checking, do not continue with speculative edits.",
	"같은 빌드 출력·DB·브라우저 자원의 선행 작업이 끝났는지 확인한다. 독립 읽기는 계속하고 쓰기는 별도 자원을 쓰거나 직렬 실행한다.":     "Check whether earlier work on the same build output, database or browser has finished. Continue independent reads, and use a separate resource or run writes one at a time.",
	"같은 공유 자원의 실행이 끝나거나 독립 자원이 확인되면 계속한다.":                                           "Continue when the run on the shared resource ends or an independent resource is confirmed.",
	"연결된 세션의 동일 대상·결과 근거를 합치고, 새 입력·환경·결과를 확인할 다음 행동을 선택한다.":                         "Combine the evidence for the same target and result from linked sessions, and choose a next action that checks new input, environment or results.",
	"새 근거가 없으면 같은 재시도를 멈추고 남은 질문이나 실패 원인을 정리한다.":                                     "If there is no new evidence, stop retrying and write up the remaining questions or the cause of failure.",
	"미해결 질문 하나와 이미 얻은 근거를 대조하고, 새 근거가 필요한 검색만 선택한다.":                                 "Compare one open question with the evidence you have, and choose only searches that need new evidence.",
	"같은 자료뿐이면 검색을 멈추고 현재 근거와 남은 질문을 정리한다.":                                           "If only the same material turns up, stop searching and write up the current evidence and remaining questions.",
	"같은 리뷰를 재위임하기 전에 새 관점·추가 근거·담당자를 정리한다.":                                          "Before delegating the same review again, work out the new perspective, added evidence and owner.",
	"새 근거가 없으면 현재 결과를 합치고 남은 완료 조건을 확인한다.":                                           "If there is no new evidence, combine the current results and check the remaining completion criteria.",
	"실패한 검사를 고치거나, 마지막 변경에 맞는 가장 작은 검사를 직접 실행해 결과를 확인한다.":                            "Fix the failing check, or run the smallest check that fits the last change and read the result.",
	"외부 원인(권한·네트워크·외부 서비스)이나 사용자의 중단 지시가 있을 때만 근거와 함께 보고하고 멈춘다.":                     "Report with evidence and stop only for an external cause (permissions, network, outside service) or the user's instruction to stop.",
	"요청 항목별 결과와 누락을 대조하고, 승인 범위 안에서 남은 작업을 계속한다.":                                    "Compare results and gaps for each requested item, and continue the remaining work within the approved scope.",
	"부분 산출물과 전체 완료를 구분하고 항목별 근거를 남긴다.":                                               "Keep partial output distinct from overall completion and leave evidence for each item.",
	"이미 명시된 승인 범위를 확인하고 그 안의 다음 작업을 진행한다.":                                           "Check the approval already given and continue with the next task inside it.",
	"범위·외부 영향·복구 가능성이 바뀌는 선택만 사용자에게 확인한다.":                                           "Confirm with the user only choices that change scope, external impact or recoverability.",
	"관측 사실만 전달한다(처방 없는 비교군).":                                                        "Deliver observed facts only (comparison group with no prescription).",
	"자동 복구 처방을 추가하지 않는다.":                                                            "Add no automatic recovery prescription.",
}

// Localize returns a prescription with its sentences in the given language.
func Localize(p Prescription, l string) Prescription {
	if l != lang.English {
		return p
	}
	if s, ok := english[p.Action]; ok {
		p.Action = s
	}
	if s, ok := english[p.StopCondition]; ok {
		p.StopCondition = s
	}
	return p
}
