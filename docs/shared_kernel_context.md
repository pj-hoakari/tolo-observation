# 共有カーネル 入出力整理（tolo.kernel.v1）

ドメイン定義: shared_kernel_domain.md
共有カーネルはデプロイ単位を持たないため、受け渡し DTO の proto はここを正本とする
生成は観測、グラフ構造の正本はグラフ編集、消費は Flow Control と Line Control

## 利用箇所

| 型 | 供給 | 消費 | 経路 |
|---|---|---|---|
| Graph | グラフ編集（Graph Authoring） | 観測、Flow、Line | 観測が現在の版を取得し最適化要求に同梱 |
| ObservationSnapshot | 観測（observation_spec.md） | Flow、Line | 最適化要求に同梱 |
| RiskLocation／DangerFlag | Flow が判定／スタッフが宣言 | Flow、Line、スタッフ、ゲスト | 提案・状況表示に含まれる |
| GraphAnchor | 配置の定義側（グラフ編集・Operation） | 管理 UI、Guest Service、観測（GetObservationPointMappings 経由。Flow／Line へは渡らない） | 配置位置の共有語彙（複数配置可。ルート途中は表示用の比率） |

危険度（Risk Level）は Flow Control 専用のため本 package には置かない（`tolo.flow.v1` 内部）
ゲートと行列はカーネルに持ち込まない（`tolo.line.v1` で定義）

## 参考 proto 定義

```proto
syntax = "proto3";
package tolo.kernel.v1;
import "google/protobuf/timestamp.proto";

// event_id はテナントが発番する公開 ID（16 文字 hex）
// point_id／route_id はグラフ編集のグラフ文書に由来する識別子（エディタ採番。イベントの会場グラフ内で一意。公開 ID の発番規約の対象外）

// グラフ: 会場をポイントとルートで表す有向グラフ（構造の正本はグラフ編集）
message Graph {
  string event_id = 1;        // グラフはイベント単位
  string revision_id = 2;     // グラフ版（下流は現在の版を消費）
  repeated Point points = 3;
  repeated Route routes = 4;
}

// ポイント: 停滞箇所または分岐合流の結節点
message Point {
  string point_id = 1;
  PointType type = 2;
  bool is_boundary = 3;       // 入退出点
  bool boundary_active = 4;   // 有効な入退出点か（Open/Closed モード判定に使用）
}

enum PointType {
  POINT_TYPE_UNSPECIFIED = 0;
  POINT_TYPE_GOAL = 1;                // 目標
  POINT_TYPE_GOAL_TRANSIT_MIXED = 2;  // 通過目標混在
  POINT_TYPE_TRANSIT_ONLY = 3;        // 通過専用
}

// ルート: ポイント間の通路
message Route {
  string route_id = 1;
  string from_point_id = 2;
  string to_point_id = 3;
  DirectionAttribute direction = 4;   // 方向属性
  optional double capacity_hint = 5;  // 容量ヒント（人/分）
}

enum DirectionAttribute {
  DIRECTION_ATTRIBUTE_UNSPECIFIED = 0;
  DIRECTION_ATTRIBUTE_ONE_WAY = 1;    // 一方通行（from→to）
  DIRECTION_ATTRIBUTE_BOTH_WAYS = 2;  // 両通行
}

// 観測スナップショット: 観測ウィンドウ確定分（正準単位 人/分）
message ObservationSnapshot {
  string snapshot_id = 1;
  string event_id = 2;
  google.protobuf.Timestamp window_start = 3;
  google.protobuf.Timestamp window_end = 4;
  repeated PointScore point_scores = 5;
  repeated RouteScore route_scores = 6;
}

// 人数スコア（ポイントの占有レベル）と占有量変化（ΔOcc）
message PointScore {
  string point_id = 1;
  double people_score = 2;
  double occupancy_delta = 3;
  bool exhaustive = 4;  // 悉皆計測か。false のスコアは容量比較・ポイント間比較に使えない
}

// 流量スコア（方向別またはスカラー）と停滞量スコア
message RouteScore {
  string route_id = 1;
  oneof flow {
    DirectionalFlow directional_flow = 2;
    double scalar_flow = 3;
  }
  double stagnation_score = 4;    // 全体−流量（高停滞の主情報源）
  optional double turn_ratio = 5; // 方向転換率（原則内部導出。観測は任意）
  bool exhaustive = 6;            // 悉皆計測か。false のスコアは容量比較・ポイント間比較に使えない
}

message DirectionalFlow {
  double forward = 1;   // from→to
  double backward = 2;  // to→from
}

// グラフ要素参照: ポイントまたはルートの参照（種類の意味づけは利用側が持つ）
// 危険の種類を伴わない位置参照（トリガー発火位置・検知状態・混雑報告等）に使う
message LocationRef {
  oneof target {
    string point_id = 1;
    string route_id = 2;
  }
}

// グラフ上の配置位置: 配置系（観測点紐づけ・QR 設置箇所・スタッフ配置）の共有語彙
// 同一のポイント／ルートへの複数配置を許す（配置は各々が独立の識別子を持つ）
// route_position は表示用途のみ（最適化・観測スコアの算出・按分には使わない）
message GraphAnchor {
  oneof target {
    string point_id = 1;
    string route_id = 2;
  }
  optional double route_position = 3;  // ルート対象時の位置（from→to の比率 0.0〜1.0。表示用途のみ）
}

// 危険箇所: パンクまたは高停滞のポイント／ルート
message RiskLocation {
  oneof target {
    string point_id = 1;
    string route_id = 2;
  }
  RiskKind kind = 3;
}

enum RiskKind {
  RISK_KIND_UNSPECIFIED = 0;
  RISK_KIND_PUNCTURE = 1;         // パンク（容量ヒント持ちスカラールートが容量到達）
  RISK_KIND_HIGH_STAGNATION = 2;  // 高停滞（履歴パーセンタイル以上かつ上昇幅一定以上）
}

// 危険フラグ: スタッフ／オーナーの手動危険宣言
message DangerFlag {
  oneof target {
    string point_id = 1;
    string route_id = 2;
  }
  bool active = 3;
  string operated_by = 4;  // user_id
  google.protobuf.Timestamp operated_at = 5;
}
```

## 不変条件との対応

- スコア比較は同一イベント内の過去スコアに対してのみ → 履歴（時系列・統計・最適化履歴）は観測が永続化層（PostgreSQL の時系列テーブル）から同一イベント分のみ切り出して Flow／Line へ同梱する（Flow Control）
- 受け渡しは正準単位（人/分）に正規化 → 正規化は観測の責務（計測値受信時）
- スコアの悉皆性は計測方式で決まる → カメラ方式の観測点は `exhaustive = true`、QR 方式は `false`（observation_spec.md）
  `exhaustive = false` のスコアは採取率が未知のため絶対量として比較できない。消費側は時系列比較にのみ用いる
- グラフは Flow が保持せず外部が毎回渡す → `OptimizeRequest` に Graph を必ず同梱
- 共有カーネル自体は固有のドメインイベントを持たない → 本ファイルにイベント対応表はない
