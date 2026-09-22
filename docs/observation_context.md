# 観測コンテキスト 入出力整理

本コンテキストは1つだが、デプロイ単位は Observation と Edge Bridge Service の2つに分かれる。
Edge Bridge Service は WebRTC のシグナリングのみを担い、Service Gateway を経由しない完全に独立した位置に置く。常時接続を伴うシグナリングを、単発の RPC を型付き委譲で中継する Gateway に載せないためである。

ドメイン定義: observation_domain.md
デプロイ単位: observation_spec.md、Edge Bridge Service（WebRTC シグナリング）
対象外: WebRTC の映像そのもの（映像の中継と、映像越しの観測点グラフ紐づけの操作。ページ間で直接やりとりする）

## ドメインイベント↔RPC 対応

| ドメインイベント | 対応 |
|---|---|
| EdgeDeviceRegistered／ObservationPointRegistered | EdgeDeviceService.RegisterEdgeDevice |
| EdgeDeviceUnregistered | EdgeDeviceService.UnregisterEdgeDevice |
| EdgeDeviceStarted／EdgeDeviceDisconnected | EdgeDeviceService.Heartbeat（開始・途絶判定） |
| ObservationPointDisabled／ObservationPointReEnabled | 内部（Heartbeat 途絶・回復から波及） |
| ObservationPointReconfigured | EdgeDeviceService.UpdateObservationPointConfig（スタッフアプリから直接） |
| MeasurementReceived | MeasurementIngestService.ReportMeasurements |
| SnapshotTaken | 内部（観測ウィンドウ確定） |
| OptimizationRequested | 内部→ Flow.Optimize／Line.GuideQueues の呼び出し |
| OptimizationResultPersisted | 内部（最適化応答の永続化） |
| AccessRequested | Edge Bridge Service のシグナリング（Edge Bridge Service。RPC は策定中） |

## 手動介入・運用状態変更の受け口（誘導ドメインの現場誘導接点）

| ドメインイベント（所属先） | 対応 RPC |
|---|---|
| GateOpened／GateClosed（Line 所属） | ManualInterventionService.OperateGate |
| DangerFlagToggledByOperator（Flow 接点） | ManualInterventionService.ToggleDangerFlag |
| ScheduleEventRegistered（Flow 接点） | ManualInterventionService.RegisterScheduleEvent |
| CongestionManuallyReported（Flow 接点） | ManualInterventionService.ReportCongestion |
| QueueManuallyCorrected（Line 所属） | ManualInterventionService.CorrectQueue |

## 他コンテキストとの接点

| 接点 | RPC |
|---|---|
| グラフ・紐づけ・設計時ゲート指定・QR 設置箇所の取得 | GraphSupplyService.GetCurrentRevision／GetObservationPointMappings／GetGatePoints／GetQrLocations（QR 設置箇所は QR 方式の観測点として読み替える） |
| 大域最適化の要求 | FlowControlService.Optimize（大域フローコントロールコンテキスト） |
| 局所行列誘導の要求 | LineControlService.GuideQueues（局所行列誘導コンテキスト） |
| 提案の配信依頼・フィードバック引き渡し | DeliveryCoordinationService.RequestProposalDelivery／RecordFeedbackValues |
| 参照値の取得（コールドスタート） | ReferenceAggregationService.GetReferenceValues |
| 設定値・履歴期間の参照 | TenantService.GetObservationSettings |
| ゲストへの状況提供 | `guest-status` トピックへ publish（PubSub。ID と数値のみ、表示名は付与しない。行列数値は Line の guest_digest を転送）。GetGuestSnapshot は復旧・突き合わせ用 |
| スタッフへの状況提供 | StatusQueryService.GetEventOverview |
