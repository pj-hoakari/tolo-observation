# tolo-observation

## 開発

### セットアップ

mise を使用して開発環境をセットアップする  
`mise install` の後処理で lefthook の pre-commit フック（シークレット検出と golangci-lint）が導入される

```bash
mise trust
mise install
cp .env.example .env
task proto
```

`.env` は Compose と `task migrate:*` が開発用 PostgreSQL の接続情報（`POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB`）を読む先で、git 管理外

### テストと lint

```bash
go build ./...
go test ./...
golangci-lint run
```

`go test ./...` は統合テストで PostgreSQL コンテナを起動するため、Docker が必要

### Docker Compose での起動

Docker Compose で PostgreSQL、golang-migrate によるマイグレーション、および開発サーバーを起動できる

```bash
docker compose up --build
```

もしくは
```bash
task up:build
```

サーバーは `http://localhost:8080`、PostgreSQL は `localhost:5432` で待ち受ける（停止は `task down`）  
Compose の `migrate` サービスは `Dockerfile` の `migrate` ターゲットをビルドして起動するため、`migrations/` を追加・変更したあとは `--build` 付き（`docker compose up --build` または `task up:build`）で起動し直す  
`task up` はイメージを再ビルドしないので、古いマイグレーションのままになる  
Compose は依存先の代わりに開発専用のモック `mockdeps`（`cmd/mockdeps`）を起動する。ホストからは `http://localhost:8089` で届く  
`server` の `GRAPH_AUTHORING_URL`、`FLOW_CONTROL_URL`、`OPERATION_URL`、`INTERNAL_JWKS_URL` はすべて `mockdeps` を指す  
`mockdeps` は 1 つのポートで次を提供する

- GraphSupplyService の `GetCurrentRevision`、`GetObservationPointMappings`、`GetQrLocations`。リクエストの内容と認証によらず、固定のグラフを返す
  - リビジョンは `mock-revision-1` で、地点 `point-entrance`（`POINT_TYPE_TRANSIT_ONLY`）と `point-hall`（`POINT_TYPE_GOAL`）、その間を双方向に結ぶルート `route-entrance-hall` からなる
  - QR 設置場所は `a1b2c3d4e5f60718` の 1 件で、`route-entrance-hall` に紐づく
  - 観測点の ID は端末の登録時に本サービスが採番するため、モックからは事前に分からない。そのため `GetObservationPointMappings` は空のマッピングを返し、エッジ端末の計測値はスナップショットのグラフに載らない
  - `GetDisplayNames` と `GetGatePoints` は本サービスが呼ばないので、`unimplemented` を返す
- FlowControlService の `Optimize`。常に `VERDICT_SKIPPED_NO_TRIGGER` と空の検知状態を返す。最適化結果には `route-entrance-hall` の重要度を 1 件だけ載せ、`solved_at` は要求の `server_time` とする。フィードバック値には `tau_star` だけを載せる
- DeliveryCoordinationService の `RequestProposalDelivery`。受け取ったテナント、イベント、ルート重要度の件数をログに出し、配信数として `realtime_count` に 1 を返す
- DeliveryCoordinationService の `RecordFeedbackValues`。受け取ったテナント、イベント、エントリの件数をログに出し、空の応答を返す
- `GET /.well-known/jwks.json` と `POST /token`。起動時に生成した ES256 鍵の公開鍵を配り、その鍵で内部 JWT を発行する（「内部 JWT」を参照）

`mockdeps` は呼ばれた RPC の procedure をログに出すので、`docker compose logs mockdeps` で依存先への呼び出しを確かめられる  
依存先の実サービスとつないだ検証は tolo リポジトリの統合環境で行う

### 環境変数

| 環境変数 | デフォルト | 内容 |
| --- | --- | --- |
| `SERVER_ADDR` | `:8080` | 待ち受けるアドレス |
| `DATABASE_URL` | なし（必須） | PostgreSQL の接続先 |
| `OBSERVATION_PAGE_URL_TEMPLATE` | なし（必須） | 端末登録時に返す観測ページ URL のテンプレート。`{tenant_id}`、`{event_id}`、`{edge_device_id}` をすべて含め、登録した端末の公開 ID で置き換えて返す |
| `HEARTBEAT_TIMEOUT` | `30s` | エッジ端末の `online`、切断、観測点の実効的な `enabled` を判定する Heartbeat の鮮度。`time.ParseDuration` が解釈する形式を取る |
| `HEARTBEAT_LOSS_HOLD_THRESHOLD` | `0.8` | 切断を保留する、Heartbeat が同時に途絶えた端末の割合の閾値。0 より大きく 1 以下 |
| `HEARTBEAT_LOSS_HOLD_MIN_DEVICES` | `10` | 保留を判定する母数の下限。母数は直前の `HEARTBEAT_TIMEOUT` の 2 倍の間に Heartbeat を送った、全テナントの登録済み端末 |
| `HEARTBEAT_LOSS_HOLD_MAX_DURATION` | `5m` | 保留の上限時間。保留の開始からこの時間を過ぎたら、途絶が続く端末を通常どおり切断する |
| `GRAPH_AUTHORING_URL` | なし（必須） | GraphSupplyService の呼び出し先。Service Gateway の internal listener（compose では `mockdeps` の `http://mockdeps:8080`）を指す。public listener は GraphSupplyService を拒否する。受領した内部 JWT をそのまま `Authorization` に載せて呼ぶ |
| `FLOW_CONTROL_URL` | なし（必須） | FlowControlService の呼び出し先（compose では `http://mockdeps:8080`）。gRPC で、認証情報を付けずに呼ぶ |
| `OPERATION_URL` | なし（必須） | DeliveryCoordinationService の呼び出し先。Service Gateway の internal listener（compose では `mockdeps` の `http://mockdeps:8080`）を指す。`GRAPH_AUTHORING_URL` と同じく、受領した内部 JWT をそのまま `Authorization` に載せて呼ぶ |
| `OBSERVATION_WINDOW` | `1m` | スナップショットに含める計測値の期間（`window_end` がこの期間内のもの） |
| `OPTIMIZE_TIMEOUT` | `30s` | Optimize 呼び出しのタイムアウト。超過や失敗はログに残し、計測値の受け入れは成功させる |
| `OPERATION_TIMEOUT` | `5s` | Operation 呼び出しのタイムアウト。Optimize の後に呼び、超過や失敗はログに残して観測サイクルを続ける |
| `INTERNAL_JWKS_URL` | `http://gateway:8080/.well-known/jwks.json` | 内部 JWT の検証に使う JWKS の取得先（compose では `http://mockdeps:8080/.well-known/jwks.json`） |
| `INTERNAL_JWT_ISSUER` | `service-gateway` | 内部 JWT に期待する `iss` |
| `INTERNAL_JWT_AUDIENCE` | `tolo-observation` | 内部 JWT に期待する `aud` |
| `LOG_LEVEL` | `info` | ログに出力する最小レベル。`debug`／`info`／`warn`（`warning` も同義）／`error`／`critical` を取り、未知の値ならサーバーは起動しない |
| `GOOGLE_CLOUD_PROJECT` | なし | 設定するとログの `logging.googleapis.com/trace` を `projects/<project>/traces/<trace_id>` 形式にし、Cloud Logging でトレースと相関させる。未設定なら素のトレース ID を出力する |
| `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | なし | どちらかを設定したときだけ OTLP/HTTP で span を export する。未設定ならトレーシングは無効 |
| `OTEL_SERVICE_NAME` | `tolo-observation` | トレースの `service.name` |

ヘッダ・TLS・タイムアウトなどその他の `OTEL_EXPORTER_OTLP_*` は exporter がそのまま解釈する  
ログは標準出力へ 1 行 1 件の JSON で書き出す

### マイグレーション

ローカルでマイグレーションを実行する場合は、Compose で PostgreSQL を起動してから次を実行する（接続先は `DATABASE_URL` で上書きできる）  
`task migrate:*` が使う golang-migrate CLI は `.config/mise/conf.d/db.toml` で管理しており、`mise install` で `mise.toml` のツールと一緒に導入される

```bash
# マイグレーションを適用
task migrate:up
# 新しいマイグレーションを作成（up/down のペアを生成）
task migrate:create -- <migration_name>
# 1 つ前にロールバック
task migrate:down
# 現在のバージョンと dirty 状態を表示
task migrate:version
```

### トレースの確認（Jaeger）

監視スタックはオーバーライドファイル `compose.o11y.yml` を重ねたときだけ有効になる  
Jaeger が起動し、`server` に OTLP エクスポート用の環境変数（`OTEL_EXPORTER_OTLP_ENDPOINT` など）がセットされる

```bash
docker compose -f compose.yml -f compose.o11y.yml up --build
```

もしくは
```bash
task up:build:o11y
```

Jaeger UI は `http://localhost:16686`（停止は `task down:o11y`）

### 内部 JWT

RPC を呼び出すには Service Gateway 発行の内部 JWT が必要である  
Compose で起動したときは、`mockdeps` の `POST /token` でトークンを発行する。`server` は `mockdeps` の JWKS で検証するので、そのまま `Authorization: Bearer <token>` で呼び出せる

```bash
TOKEN=$(curl -s -X POST localhost:8089/token \
  -d '{"tenant_id":"0123456789abcdef","event_id":"fedcba9876543210","scope":"events.report"}' | jq -r .token)

# QR の計測値を報告すると、GraphSupplyService と Optimize の呼び出しを経て optimization_results に 1 行残り、続いて Operation の RequestProposalDelivery と RecordFeedbackValues が呼ばれる
curl -s -X POST localhost:8080/tolo.observation.v1.MeasurementIngestService/ReportMeasurements \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d "{\"eventId\":\"fedcba9876543210\",\"tenantId\":\"0123456789abcdef\",\"measurements\":[{\"source\":\"MEASUREMENT_SOURCE_QR\",\"qrLocationId\":\"a1b2c3d4e5f60718\",\"windowStart\":\"$(date -u -v-1M +%FT%TZ)\",\"windowEnd\":\"$(date -u +%FT%TZ)\",\"countIn\":3,\"countOut\":1}]}"
```

`POST /token` は次のキーを持つ JSON を受け取り、`go tool jwtgen` と同じ `token` / `claims` / `jwks` を返す。`iss` は `service-gateway` に固定する

| キー | 既定値 | 内容 |
| --- | --- | --- |
| `audience` | `tolo-observation` | `aud` |
| `token_use` | `event_access` | `tenant_access`、`event_access`、`registration`、`service` のいずれか |
| `tenant_id` | なし | テナントの公開 ID（16 文字 hex） |
| `event_id` | なし | イベントの公開 ID（16 文字 hex） |
| `scope` | なし | 空白区切りの scope |
| `ttl` | `10m` | 有効期間。`time.ParseDuration` が解釈する形式を取る |

- 本サービスの RPC はいずれも `event_access` を要求し、`ReportMeasurements` だけが `service` も受け付ける
- `event_access` では `tenant_id`、`event_id`、`scope` が必須である。必要な scope（`events.read`／`events.report`／`events.manage`／`events.operate`）は proto の `required_scopes` で宣言されている

`mockdeps` は再起動のたびに鍵を作り直すが、`kid` は変わらない。`server` は取得した JWKS を最長 5 分キャッシュし、既知の `kid` では取り直さないので、`mockdeps` だけを再起動したときは `server` も再起動する

Compose を使わずにトークンだけが必要なときは、`internal-jwt-handling` 同梱の jwtgen CLI でトークンと JWKS を生成できる（`go tool jwtgen -h` でフラグを確認できる）  
この場合は出力の `jwks` を任意の HTTP エンドポイントで配信し、`INTERNAL_JWKS_URL` にその URL を設定する

### tolo-kernel-proto と tolo-graph-authoring の proto

`tolo/kernel/v1/kernel.proto` の Go コードは自前で生成せず、`github.com/pj-hoakari/tolo-kernel-proto` の生成コードを使う  
`task proto:write-kernel` が、go.mod が参照する版の `kernel.proto` を `proto/`（git 管理外）に書き出す  
GraphSupply のクライアントには `github.com/pj-hoakari/tolo-graph-authoring` の生成コードを使う  
同じ proto ファイルを 2 つの Go パッケージが登録すると、両方を読み込むプロセス（Gateway など）が起動時に panic する

### tolo-flow-control の proto

tolo-flow-control の proto は `Taskfile.yml` の `FLOW_CONTROL_PROTO` が指すアーティファクトから取得する  
`task proto:gen:go` が `.proto-deps/tolo-flow-control`（git 管理外）に展開し、クライアントを `gen/tolo/flow` に生成する  
展開先には `kernel.proto` も書き出すが、kernel の Go コードは生成しない  
`FLOW_CONTROL_PROTO` のタグとダイジェストは Renovate が更新する  
更新の PR では `gen/` が古くなり proto-gen-check が失敗するので、その PR のブランチで `task proto:gen:go` を実行してコミットする

ローカルの proto で生成する場合は、`FLOW_CONTROL_PROTO_DIR` に proto のルート（`tolo/flow/v1/flow.proto` を含むディレクトリ）を指定する  
`.env` に書いてもよい

```bash
FLOW_CONTROL_PROTO_DIR=../tolo-flow-control/proto task proto:gen:go
```

### connect-es の生成

connect-es の生成（`task proto:gen:es`）はリリース時に CI で行う  
ローカルで実行する場合は `clients/connect-es` の依存（`npm i`）を導入する必要がある

## イメージからマイグレーションを実行する

`Dockerfile` の `migrate` ターゲットは golang-migrate CLI のイメージに `migrations/` を `/migrations` として同梱したものである  
リポジトリを clone しなくても、このイメージだけで DB マイグレーションを実行できる  
`ENTRYPOINT` は `migrate -path /migrations` なので、利用者は接続先とコマンドだけを引数として渡す

ローカルでビルドする場合は次のとおりである

```bash
docker build --target migrate -t tolo-observation-migrate .
```

適用は `-database` に接続先を、続けてコマンドを渡す

```bash
docker run --rm --network host tolo-observation-migrate -database "$DATABASE_URL" up
```

公開イメージは `ghcr.io/<owner>/<repo>-migrate` で、サーバーのイメージと同じバージョンタグを付ける

```bash
docker run --rm --network host ghcr.io/<owner>/<repo>-migrate:<version> -database "$DATABASE_URL" up
```

そのほかのコマンドも同じ形で渡す

```bash
# 現在のバージョンを確認
docker run --rm --network host ghcr.io/<owner>/<repo>-migrate:<version> -database "$DATABASE_URL" version
# 1 つ前にロールバック
docker run --rm --network host ghcr.io/<owner>/<repo>-migrate:<version> -database "$DATABASE_URL" down 1
```

golang-migrate CLI は接続先を環境変数からは読まないため、`-database` は必ず引数で渡す  
`--network host` はコンテナからホスト上の PostgreSQL に接続するための指定であり、接続先がホスト外にあるなら不要である

## proto アーティファクトの利用

`.proto` は [ORAS](https://oras.land) で OCI アーティファクト化され、GitHub Container Registry に公開される  
アーティファクト名: `ghcr.io/<owner>/<repo>-proto`

### 取得（pull）

[ORAS CLI](https://oras.land/docs/installation) が必要（mise でも導入される）

```bash
# 出力先ディレクトリに proto を展開（ディレクトリ構造が復元される）
oras pull ghcr.io/<owner>/<repo>-proto:latest -o proto
```

取得した `.proto` は `buf` や `protoc` の入力としてそのまま利用できる

`tolo/kernel/v1/kernel.proto` はこのアーティファクトに含まれない  
`kernel.proto` は、go.mod が参照する `github.com/pj-hoakari/tolo-kernel-proto` と同じ版を `ghcr.io/pj-hoakari/tolo-kernel-proto` から取得する
