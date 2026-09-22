# Observation 入出力仕様

package: `tolo.observation.v1`
実現するコンテキスト: observation_context.md（ドメイン定義 observation_domain.md）
役割: 計測値の収集・永続化と、誘導サービス（Flow／Line）のオーケストレータ
手動介入（現場誘導のシステム接点）と運用状態の変更（ゲート開閉・観測点設定変更）も本サービスが直接受け、次回の最適化・誘導要求に反映する

対象外: WebRTC のシグナリング（別デプロイ単位の Edge Bridge Service が担う）と、映像の中継・映像越しの設定変更（ページ間で完結しマイクロサービスを介さない）
エッジ端末のライフサイクル管理（登録・登録解除・ハートビート）は本仕様に含む

## RPC 一覧

### 計測値の受け入れ（エッジ端末・Guest Service から）

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| ReportMeasurements | 観測点からの計測値（人数カウント）を受け取り、正準単位（人/分）へ正規化して永続化する。QR（設置箇所×種類ごとの発行 URL）由来の人数計上も計測源のひとつ | エッジ端末（ブラウザ、Connect）、Guest Service | `event_access` + events.report／サービス間（token_use=service） | MeasurementReceived／GuestCountedViaQr |

### エッジ端末・観測点の管理（WebRTC 除く）

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| RegisterEdgeDevice | エッジ端末を登録する。登録は実質的に観測点登録を兼ねる | 管理 UI（オーナー／スタッフ） | `event_access` + events.manage | EdgeDeviceRegistered／ObservationPointRegistered |
| UnregisterEdgeDevice | エッジ端末を登録解除する。撤去した端末を運用から外す操作。配下観測点は観測不能化する | 管理 UI（オーナー／スタッフ） | `event_access` + events.manage | EdgeDeviceUnregistered／ObservationPointDisabled |
| ListEdgeDevices | イベント配下のエッジ端末と観測点の一覧を返す（管理 UI の表示用。グラフ紐づけは Graph Authoring の GetGraph 側） | 管理 UI（オーナー／スタッフ） | `event_access` + events.read | （参照のみ） |
| Heartbeat | エッジ端末の稼働を通知する。途絶が一定時間超過で切断・観測不能化に波及 | エッジ端末 | `event_access` + events.report | EdgeDeviceStarted／EdgeDeviceDisconnected／ObservationPointDisabled／ObservationPointReEnabled |
| UpdateObservationPointConfig | スタッフ操作による観測点設定変更を反映する（誘導への介入と同じく本サービスが直接受ける） | スタッフアプリ | `event_access` + events.operate | ObservationPointReconfigured |

### 手動介入・運用状態の変更（現場誘導のシステム接点。誘導ドメイン所属）

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| OperateGate | ゲート（窓口）を開設・閉鎖する。開閉とサービスレート設定値は次回の局所行列誘導の入力になる（誘導への介入として本サービスが直接受ける） | スタッフアプリ | `event_access` + events.operate | GateOpened／GateClosed（局所行列誘導所属） |
| ToggleDangerFlag | 危険フラグ（手動危険宣言）を立てる・下げる。即時トリガーとして次回最適化要求に同梱 | スタッフアプリ | `event_access` + events.operate | DangerFlagToggledByOperator（Flow 側 DangerFlagRaised/Lowered の契機） |
| RegisterScheduleEvent | スケジュール（開演等の予定）を登録する。スケジュールトリガーの源 | スタッフアプリ | `event_access` + events.operate | ScheduleEventRegistered |
| ReportCongestion | 混雑を手動報告する | スタッフアプリ | `event_access` + events.operate | CongestionManuallyReported |
| CorrectQueue | 行列状態を手動補正する（局所行列誘導へ渡す） | スタッフアプリ | `event_access` + events.operate | QueueManuallyCorrected |

### 状況の参照

| RPC | 説明（ユビキタス言語） | 呼び出し元 | 認可 | 関連ドメインイベント |
|---|---|---|---|---|
| GetGuestSnapshot | その時点のゲスト向け状況を集約して返す。通常経路は Guest Service への push であり、本 RPC は push 欠落時の復旧・突き合わせ用 | Guest Service（復旧時のみ） | サービス間（token_use=service） | （復旧用参照） |
| GetEventOverview | スタッフ向けの詳細状況（スナップショット・提案・危険フラグ・行列状態）を返す | スタッフアプリ | `event_access` + events.read | （参照のみ） |

### 呼び出す側（本サービスがクライアントになる RPC）

| 相手 | RPC | 目的 | 関連ドメインイベント（本コンテキスト側） |
|---|---|---|---|
| Graph Authoring | GetCurrentRevision／GetObservationPointMappings／GetGatePoints | 会場グラフ・紐づけ・設計時ゲート指定の取得 | — |
| Flow Control | Optimize | 観測スナップショット確定等を契機に最適化要求（検知状態・手動介入を同梱） | OptimizationRequested／OptimizationResultPersisted |
| Line Control | GuideQueues | 行列把握・案内・形状最適化の要求 | 同上 |
| Operation | RequestProposalDelivery | 提案の配信依頼（配送の振り分けはスタッフ間コミュニケーションが担う） | — |
| Guest Service | （PubSub: `guest-status` へ publish） | スナップショット確定・行列状態更新のたびにゲスト向け状況（混雑・行列。`tolo.guest.v1.GuestStatus`。ID と数値のみ）を publish。誘導提案・表示名は含めない（提案はスタッフ向け、表示名の付与は Guest Service） | （ゲスト側 GuestViewUpdated の源） |
| Operation | RecordFeedbackValues | Flow が出力したフィードバック値の引き渡し（テナント内蓄積用） | — |
| Reference Aggregation | GetReferenceValues | コールドスタート用参照値の取得（短期イベント等） | — |
| Graph Authoring | GetQrLocations | QR 設置箇所の取得（QR 方式の観測点として扱う） | — |
| Tenant Management | GetObservationSettings | 設定値・履歴期間の参照 | — |

## 参考 proto 定義

```proto
syntax = "proto3";
package tolo.observation.v1;
import "google/protobuf/timestamp.proto";
import "tolo/kernel/v1/kernel.proto";
import "tolo/guest/v1/guest.proto";

service MeasurementIngestService {
  rpc ReportMeasurements(ReportMeasurementsRequest) returns (ReportMeasurementsResponse);
}

service EdgeDeviceService {
  rpc RegisterEdgeDevice(RegisterEdgeDeviceRequest) returns (RegisterEdgeDeviceResponse);
  rpc UnregisterEdgeDevice(UnregisterEdgeDeviceRequest) returns (EdgeDevice);
  rpc ListEdgeDevices(ListEdgeDevicesRequest) returns (ListEdgeDevicesResponse);
  rpc Heartbeat(HeartbeatRequest) returns (HeartbeatResponse);
  rpc UpdateObservationPointConfig(UpdateObservationPointConfigRequest) returns (ObservationPoint);
}

service ManualInterventionService {
  rpc OperateGate(OperateGateRequest) returns (OperateGateResponse);
  rpc ToggleDangerFlag(ToggleDangerFlagRequest) returns (ToggleDangerFlagResponse);
  rpc RegisterScheduleEvent(RegisterScheduleEventRequest) returns (RegisterScheduleEventResponse);
  rpc ReportCongestion(ReportCongestionRequest) returns (ReportCongestionResponse);
  rpc CorrectQueue(CorrectQueueRequest) returns (CorrectQueueResponse);
}

service StatusQueryService {
  // ゲスト向け状況の型は tolo.guest.v1 が正本（受信側の公開言語）。復旧・突き合わせ用
  rpc GetGuestSnapshot(GetGuestSnapshotRequest) returns (tolo.guest.v1.GuestStatus);
  rpc GetEventOverview(GetEventOverviewRequest) returns (EventOverview);
}

// ---- 計測値 ----

// 計測値: 観測点からの生データ（観測スコアの供給源）
message Measurement {
  string observation_point_id = 1;  // source=EDGE の計測単位。source=QR のときは空
  google.protobuf.Timestamp window_start = 2;
  google.protobuf.Timestamp window_end = 3;
  int32 count_in = 4;   // ウィンドウ内の流入計上
  int32 count_out = 5;  // ウィンドウ内の流出計上
  MeasurementSource source = 6;
  string qr_location_id = 7;  // source=QR のとき必須（設置箇所。正本はグラフ編集の QrLocation）。source=EDGE のときは空
}

enum MeasurementSource {
  MEASUREMENT_SOURCE_UNSPECIFIED = 0;
  MEASUREMENT_SOURCE_EDGE = 1;  // エッジ端末の人検知・追跡
  MEASUREMENT_SOURCE_QR = 2;    // QR（設置箇所×種類ごとの発行 URL）由来の人数計上
}

message ReportMeasurementsRequest {
  string event_id = 1;
  string edge_device_id = 2;  // QR 由来の場合は空
  repeated Measurement measurements = 3;
}
message ReportMeasurementsResponse {
  int32 accepted_count = 1;
}

// ---- エッジ端末・観測点 ----

// エッジ端末: Webカメラ＋PC等。人検知・人数カウントをエッジで完結
message EdgeDevice {
  string edge_device_id = 1;  // 公開 ID（ランダムな 16 文字 hex）。観測ページ URL に載る
  string event_id = 2;
  string name = 3;
  repeated ObservationPoint observation_points = 4;  // エッジ端末 ∋ 観測点（0個以上）
  bool unregistered = 5;  // 登録解除済み（論理削除）。識別子と配下観測点は保持
}

// 観測点: 計測対象（ポイント／ルート）ごとの計測単位。グラフ要素との関連はグラフ編集が保持
message ObservationPoint {
  string observation_point_id = 1;
  string name = 2;
  bool enabled = 3;  // 観測不能化＝false（カメラ方式のみ）
  MeasurementMethod method = 4;
}

// 計測方式: 観測点がどう計測するか。悉皆性が異なる
enum MeasurementMethod {
  MEASUREMENT_METHOD_UNSPECIFIED = 0;
  MEASUREMENT_METHOD_CAMERA = 1;  // エッジ端末の人検知。悉皆計測
  MEASUREMENT_METHOD_QR = 2;      // QR 設置箇所の読み取り。悉皆でない
}

message RegisterEdgeDeviceRequest {
  string event_id = 1;
  string name = 2;
  repeated string observation_point_names = 3;  // 登録は観測点登録を兼ねる
}
message RegisterEdgeDeviceResponse {
  EdgeDevice device = 1;
  string observation_page_url = 2;  // 観測ページの発行 URL（edge_device_id を含む固定 URL）
}
message UnregisterEdgeDeviceRequest {
  string event_id = 1;
  string edge_device_id = 2;
}
message ListEdgeDevicesRequest {
  string event_id = 1;
  bool include_unregistered = 2;  // 既定は false（解除済みを返さない）
}
message ListEdgeDevicesResponse {
  repeated EdgeDevice devices = 1;  // 観測点（enabled 含む）を内包
}
message HeartbeatRequest {
  string event_id = 1;
  string edge_device_id = 2;
  repeated string active_observation_point_ids = 3;
}
message HeartbeatResponse {}

message UpdateObservationPointConfigRequest {
  string event_id = 1;
  string observation_point_id = 2;
  string name = 3;
  bool enabled = 4;
}

// ---- 手動介入・運用状態の変更 ----

// ゲート開閉: スタッフ操作を本サービスが直接受ける。次回の局所行列誘導の入力に反映
message OperateGateRequest {
  string event_id = 1;
  string gate_point_id = 2;
  bool open = 3;
  optional double service_rate_config = 4;  // 設定値（人/分）。ハイブリッド出所の既定側
}
message OperateGateResponse {}

message ToggleDangerFlagRequest {
  string event_id = 1;
  tolo.kernel.v1.DangerFlag flag = 2;
}
message ToggleDangerFlagResponse {}

message RegisterScheduleEventRequest {
  string event_id = 1;
  google.protobuf.Timestamp scheduled_at = 2;
  repeated string related_point_ids = 3;
  string note = 4;
}
message RegisterScheduleEventResponse {}

message ReportCongestionRequest {
  string event_id = 1;
  oneof target {
    string point_id = 2;
    string route_id = 3;
  }
  CongestionLevel level = 4;
}
message ReportCongestionResponse {}

// Flow へは tolo.flow.v1.CongestionLevel に本サービスが変換して渡す（観測が ACL として翻訳。循環 import の回避）
enum CongestionLevel {
  CONGESTION_LEVEL_UNSPECIFIED = 0;
  CONGESTION_LEVEL_LOW = 1;
  CONGESTION_LEVEL_MID = 2;
  CONGESTION_LEVEL_HIGH = 3;
}

message CorrectQueueRequest {
  string event_id = 1;
  string queue_id = 2;
  optional double corrected_length_people = 3;  // 行列長（人数）の補正
  optional int32 corrected_lane_count = 4;      // レーン数の補正
}
message CorrectQueueResponse {}

// ---- 状況参照 ----

message GetGuestSnapshotRequest {
  string event_id = 1;
}

// ゲスト向け状況（GuestStatus・CrowdStatus・QueueStatus）は
// tolo.guest.v1 で定義（import "tolo/guest/v1/guest.proto"）。本サービスが生成し guest-status トピックへ publish する（PubSub）
// 誘導提案・表示名は含めない（表示名の付与とゲスト向け文言化は Guest Service）

message GetEventOverviewRequest {
  string event_id = 1;
}

// スタッフ向け詳細状況（ビュー。テナントの enum EventStatus＝イベント状態との同名衝突を避けて EventOverview とする）
message EventOverview {
  tolo.kernel.v1.ObservationSnapshot snapshot = 1;
  repeated tolo.kernel.v1.RiskLocation risks = 2;
  repeated tolo.kernel.v1.DangerFlag danger_flags = 3;
  // 提案・行列状態のフィールド（tolo.flow.v1.ProposalSet／tolo.line.v1.QueueState 等の永続化分）は意図的に未宣言
  // （参考 proto の範囲外とし、実装フェーズで宣言する）
}
```

## 補足

- 自テナントの計測値のみ収集・永続化（クロステナント集約は参照値集約が担う）
- 永続化先は PostgreSQL の時系列テーブル（パーティション分割）とし、本コンテキスト内部の実装詳細とする
  列指向 DWH（BigQuery／ClickHouse 等）への移行は視野に残し、分岐条件を超えたときに検討する。分岐条件の初期値は「履歴切り出しクエリの p95 が数百 ms を超える、または対象テーブルが数億行規模に達する」のいずれかとし、実測にもとづき運用で調整する
  格納対象: 計測値、観測スナップショット、最適化履歴（提案・判定・発火トリガー）、検知状態、行列状態、フィードバック値
  tenant_id／event_id で分割し、保護境界の強制点を本サービスに集約する
- Flow／Line への履歴（時系列窓・統計・最適化履歴・フィードバック）の切り出し・同梱は本サービスの責務
  Flow／Line は永続化層を直接参照しない（`OptimizeRequest.history`／`GuideQueuesRequest.history`）
  Flow／Line への呼び出しは Service Gateway を経由しない直接呼び出しとし、ワークロード資格情報を宛先が直接検証する（Flow Control、Line Control）。他のサービス間呼び出しは Service Gateway 経由のまま
- ゲスト向け状況の生成と `guest-status` トピックへの publish は本サービスの責務（スナップショット確定・行列状態更新を契機。sequence を単調増加で付与）
  publish する内容は状況（混雑・行列・並び先案内）の ID と数値のみ。表示名の付与・文言化は Guest Service の責務
  誘導提案はスタッフ向けで、Operation の配送（開＝Realtime／閉＝Notification）でのみ届く
  Guest Service はアクセス時に本サービスへ問い合わせない（復旧時の GetGuestSnapshot を除く）
  publish の失敗は再試行不要（sequence 冪等で次回 publish が回復。配信の再試行はブローカーに委譲）
  publish にはイベント単位の順序キーを付与する（トピック規約。spec-base/README.md）
- 対象イベントは全 RPC がリクエストの `event_id` で受け取る（spec-base/README.md の通信規約）。エッジ端末・観測点・QR 設置箇所を指定する RPC（ReportMeasurements、Heartbeat、UnregisterEdgeDevice、UpdateObservationPointConfig）も例外としない
  内部 JWT に `event_id` クレームがあればリクエストの `event_id` と突合し、不一致は `permission_denied` を返す
  リクエストが指す子資源（`edge_device_id`、`observation_point_id`、`Measurement.qr_location_id`）がリクエストの `event_id` に属さない場合も `permission_denied` を返す
  Guest Service からの QR 由来計上はマシン起点の `token_use=service` でクレームを持たないため、リクエストの `event_id` と `qr_location_id` の所属の一致で対象イベントを確定する
- 正準単位への正規化（人/分）は本サービスの責務。エッジはウィンドウ内の計上数を送る
- 観測点はグラフのポイントと1対1ではない。スコア算出時に紐づけ（グラフ編集所有）で変換
- エッジ端末の切断・登録解除は配下観測点の観測不能化に波及
  切断は Heartbeat 途絶で判定し、復帰すれば観測再開する（一時的）
  登録解除は `UnregisterEdgeDevice` による明示の操作であり、復帰しない（恒久的）。両者は `EdgeDevice.unregistered` で区別する
- 登録解除は論理削除であり、エッジ端末と配下観測点の識別子を保持する
  観測点を物理削除するとグラフ編集が持つ観測点とグラフ要素の対応が宙づりになるため、識別子は残す
  配下観測点は観測不能化（`enabled = false`）し、観測再開の対象から外す
- 登録解除の取り消しは設けない。撤去した端末を再び使う場合は新規登録とし、グラフ紐づけをやり直す
  機材の入れ替えは登録解除を伴わない。新しい機材に同じ観測ページ URL を設定すれば同一のエッジ端末として続く
- 登録解除済みのエッジ端末の扱い: `ReportMeasurements`／`Heartbeat` は `failed_precondition` を返す
  発行済みの観測ページ URL は無効となり、開いても計測値を送れない
  `ListEdgeDevices` は既定で解除済みを返さず、`include_unregistered` の指定時のみ含める
  既に受信済みの計測値と観測スナップショットは削除しない
- ゲートの扱い: 設計時指定（Graph Authoring の GetGatePoints が正本）を基に、未開設のゲートは閉状態の GateState として Line へ渡す
  開閉とサービスレート設定値は本サービスの OperateGate（スタッフアプリが直接呼ぶ）で上書きする
- 検知状態（Flow 所有）と行列状態（Line 所有）は本サービスが解釈せず永続化し、次回要求で返送する
  ゲスト向けの行列数値は Line の案内ダイジェスト（`GuideQueuesResponse.guest_digest`）をそのまま `tolo.guest.v1.QueueStatus` へ写して publish し、QueueState の中身には依存しない
- エッジ端末の認証・認可は IdP 発行トークン（`event_access`）による。観測ページはブラウザで動き、OIDC で認証してトークンを自動更新する
- どの端末からの計測かは、観測ページの発行 URL に含まれる `edge_device_id` で示す
  RegisterEdgeDevice 応答で端末ごとの固定 URL を返し、現場ではこれをキオスクの起動 URL やブックマークに設定する
  端末側のストレージに識別子を保持しない。ブラウザデータの消去・ITP によるストレージ削除・キオスクのクリーン起動で識別子を失わないための規約である
  機材を入れ替える場合は、新しい機材に同じ URL を設定すれば同一のエッジ端末として続く
  URL は秘密ではない。提示しても権限は生じず、計測値の送信には別途 `event_access` を要する
- `edge_device_id` は URL に載るため、テナント・イベントと同じく公開 ID（ランダムな 16 文字 hex）とし、内部主キーを外へ出さない
- 観測サイクル（スナップショット確定 → Flow／Line への要求、設定値の取得）は計測値の到着を契機に同期で回る
  タイマー駆動ではないため、後段の呼び出しは計測値を運んだリクエストの文脈の中で行われる
  最後の後段呼び出しは、入口内部 JWT の実際の `exp` より前に開始する。入口変換の `exp` は元トークンの残存時間で120秒より短くなる場合があるため、固定の TTL とは比較しない。厳密モードの求解にも、この開始期限へ収まる時間予算を与える
  `time_to_last_downstream_start_seconds` は入口から最後の後段呼び出しの開始までを計測し、入口 JWT の `exp` までの実時間予算と比較する
  `cycle_end_to_end_duration_seconds` は入口から後段呼び出しの完了までを計測し、30秒の配信 SLO と各 RPC の timeout に対する性能指標とする。JWT の TTL または `exp` をサイクル完了期限として扱わない（spec-base/nonfunctional.md）
- QR 由来計測（source=QR）は設置箇所単位で計上する（QR は設置箇所×種類ごとの発行 URL。計上は設置箇所単位）
  設置箇所は `Measurement.qr_location_id` で指定する。正本はグラフ編集の QrLocation（グラフ要素参照付き）
- QR 設置箇所は QR 方式の観測点として扱う。カメラ方式の観測点と対等であり、補完的な位置づけではない
  登録はグラフ編集の `AddQrLocation` が所有し、本サービスは `GetQrLocations` で取得して観測点として読み替える。観測点として別途登録しない
  QR 方式の観測点はエッジ端末に属さない。Heartbeat を持たないため観測不能化の対象外とし、計上がゼロでも障害と判定しない
- 一つのグラフ要素に紐づく観測点は、カメラ方式か QR 方式のどちらかに揃える。併存させない
  併存すると同一の来場者をカメラと QR で二重に数えるため。この制約はグラフ編集が紐づけ時に検証する
- 観測スコアには計測方式に応じた悉皆性を付す。QR 方式は自発的な読み取りに依存し採取率が未知のため、悉皆計測ではない
  悉皆でないスコアは容量ヒントとの比較（パンク判定）およびポイント間の大小比較に使えない。時系列比較（履歴パーセンタイルによる高停滞判定）にのみ用いる
  計上値のスコアへの反映方法（QrLocation の GraphAnchor からグラフ要素への写像を含む）は実装フェーズで決定
