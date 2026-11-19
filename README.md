# recfriio（Go 版: QRS-UT100B / SK-MTVU3S）

Linux 向けの USB デジタルテレビチューナー録画プログラム recfriio のフォークです。  
2026-11-19 に、C/C++ の recfriio を Go で書き直したものに置き換えました。  
動かせるのは、実機で確かめた次の 2 機種で、どちらも `recfriio` 1 つで扱えます。

| 機器 | 受けられるもの | 機種を決めるとき |
|---|---|---|
| Quixun QRS-UT100B（`3275:7020`） | 地デジ・CATV | `recfriio --hdp`（`recut100b` と同じ） |
| SKNET MonsterTV U3S（`3275:9015`） | 地デジ・BS・CS | `recfriio --u3s`（`recmtvu3s` と同じ） |

```bash
bin/recfriio 21 30 out.ts                  # 地デジ。空いている機器（UT100B → U3S の順）で受ける
bin/recfriio BS15_0 30 out.ts              # BS・CS は U3S で受ける
bin/recfriio --hdp --b25 27 30 nhk.ts      # UT100B に決めて、内蔵の B-CAS で外す
bin/recfriio --device /dev/bus/usb/003/044 CS2 - -   # 機器ファイルで決める（USB の ID から機種が決まる）
```

- 機種は、`--hdp`・`--u3s`（`--mtvu3s` とも書ける）、`--device` の USB の ID、チャンネルの順で決めます。どれも無ければ、受けられる機種のうち、ほかのプロセスが使っていないものを UT100B → U3S の順に開きます。
- `--b25` は、UT100B なら内蔵のスロット、U3S なら PC/SC のリーダーを使います（`--pcsc` を付けると UT100B でもリーダー）。
- 前からある `recut100b`・`recmtvu3s` も残しています。中身は同じで、機種がそれぞれ決まっているだけです。
- C/C++ 版の `--hdus` など、Go 版に無い機種は使えません。
- 環境変数 `RECFRIIO_USB_TRACE=1`（C/C++ 版と同じ名前。`RECUT100B_USB_TRACE=1` も使える）で、UT100B に送る制御転送を全部、標準エラーに書きます。

cgo・libusb・libpcsclite を使わず、Linux の usbfs と pcscd に直接話すバイナリです。  
C/C++ 版（Friio・HDUS・HDP などの古い機種を含む）は、タグ [`cpp-final`](../../tree/cpp-final) に残しています。

以下は機種ごとの説明です。まず QRS-UT100B（`recfriio --hdp`、`recut100b`）。

- recfriio（C/C++）の UT100B の部分（`--hdp`）を Go で書き直したもの

- チューナーの初期化・選局・C/N の読み取り
- UT100B の中の ASIE5606 が USB 転送でかける DES を外す（放送のスクランブルとは別のもの。外したあとも B25 のスクランブルは残る）
- B-CAS カードで ARIB STD-B25 のスクランブルを外す（`--b25`）。  
  カードは UT100B の内蔵スロットのものを使います（外付けのリーダーは `--pcsc`）。
- 付属 CD のドライバーからファームを取り出して送る（`ut100b-firmware`）

MonsterTV U3S の `recmtvu3s` は、下の「MonsterTV U3S」の章です。

## 動作確認

| 機器 | 環境 | 結果 |
|---|---|---|
| Quixun QRS-UT100B | Ubuntu 26.04（kernel 7.0）、amd64 | 下の表のとおり |

`recut100b` で確かめたのはこの 1 台だけです（U3S は下の章）。  
同じ作りの MonsterTV HDUS・HDUC などは、実機が無いので対象にしていません。

| 確かめたこと | 結果 |
|---|---|
| 制御転送の並び | recfriio が ch19 を開いて閉じるまでに送る 234 回と、1 回ずつ同じ（`internal/ut100b` の試験） |
| 受信（ch19、60 秒） | 約 64.7 万パケット、TS の誤り・欠けなし |
| 内蔵 B-CAS で復号（`--b25`、60 秒） | 全パケットのスクランブルが外れ、誤り・欠けなし |
| 内蔵 B-CAS で復号（`--b25`、30 分） | 約 1949 万パケットすべてのスクランブルが外れ、TS の誤り・欠けなし。USB の受け取りの取りこぼしなし |
| arib25（C）との照合 | 同じ入力（B25 を通す前の TS、60 秒・約 120 MB）を arib25 と Go で外した結果が、バイト単位で同じ（`tools/arib25-compare`） |
| ファームの取り出し | 付属 CD の 2 つのローダーから、知っている 3 つのファームがハッシュどおりに取り出せる |
| ファームの送り込み（`--firmware`） | 抜き差しのあと、取り出したファームを送って `3275:7020` で出直し、続けて `--b25` で 60 秒受けた。全部のスクランブルが外れ、誤り・欠けなし |
| 外付けのカードリーダー（`--pcsc`） | ACS ACR39U（pcscd 経由）で `--b25` を 20 秒。全部のスクランブルが外れ、誤り・欠けなし |

まだ確かめていないこと:
- 30 分より長い受信。

## 作り方

Go 1.24 以上が要ります。

```bash
CGO_ENABLED=0 go build -o bin/ ./cmd/...
```

手元に Go を入れない場合は、Docker の Go で作れます。

```bash
scripts/go.sh build -o bin/ ./cmd/...
```

## 準備

### USB の権限

udev で、UT100B の 2 つの USB ID に書き込み権限を与えます。

```text
# /etc/udev/rules.d/60-qrs-ut100b.rules
SUBSYSTEM=="usb", ENV{DEVTYPE}=="usb_device", ATTR{idVendor}=="1738", ATTR{idProduct}=="5211", MODE="0660", GROUP="plugdev"
SUBSYSTEM=="usb", ENV{DEVTYPE}=="usb_device", ATTR{idVendor}=="3275", ATTR{idProduct}=="7020", MODE="0660", GROUP="plugdev"
```

### ファーム

UT100B は電源が入るたびに、ファームの無い状態（`1738:5211`）で起動します。  
ファームを送ると `3275:7020` として出直します。  
ファームはメーカーの著作物なので、このリポジトリには含めていません。  
製品に付いていた CD の `Driver` フォルダーにある `Quixun_AS11Loader.sys` から取り出します。

```bash
bin/ut100b-firmware extract /path/to/CD/ROBRO_Origin/Driver/Quixun_AS11Loader.sys -o asv5211-firmware.bin
bin/ut100b-firmware load asv5211-firmware.bin
```

`recut100b --firmware asv5211-firmware.bin ...` とすると、ファームの無い UT100B しか無いときに送ってから使います。

## 使い方

引数の形は C/C++ 版の recfriio・recpt1 と同じです。

```bash
bin/recfriio [オプション] チャンネル 秒 出力先
```

| 引数 | 意味 |
|---|---|
| チャンネル | `13`〜`62`（UHF）、`C13`〜`C63`（CATV。`K13` とも書ける。UT100B だけ）、`BS01_0`〜`BS23_7`・`CS2`〜`CS24`（U3S だけ） |
| 秒 | 録る長さ。`-` なら止められる（SIGINT・SIGTERM）まで |
| 出力先 | 書き出すファイル。`-` なら標準出力 |

| オプション | 意味 |
|---|---|
| `--hdp` / `--u3s`（`--mtvu3s`） | 機種を UT100B / U3S に決める（`recut100b`・`recmtvu3s` には無い） |
| `--b25` | B-CAS カードでスクランブルを外す。UT100B は内蔵のスロット、U3S は PC/SC のリーダー |
| `--pcsc` | `--b25` で、外付けのカードリーダーを使う（`--b25` も付いたことになる。pcscd の窓口は `PCSCLITE_CSOCK_NAME` で変えられる） |
| `--stats` | 終わりに TS のヘッダーを数えた結果（同期・TEI・連続カウンター・PID・PAT の TSID）を書く |
| `--round N` | MULTI2 の段数（既定 4） |
| `--strip` | ヌルパケットを捨てる（UT100B はもともと出さない） |
| `--sync` | `--b25` で、鍵がそろうまでの失敗を数えない |
| `--device PATH` | 使う機器（`/dev/bus/usb/BBB/DDD`）。USB の ID から機種が決まる。既定は空いているもの |
| `--firmware FILE` | （UT100B）ファームの無い UT100B しか無ければ、これを送ってから使う |
| `--no-settle` | （U3S）受け始めの、復調部が落ち着く前のパケットも捨てずに書き出す |
| `--no-descramble` | （U3S）USB 転送の暗号を外さずに書き出す（照合用） |
| `--raw FILE` | B25 を通す前の TS も書き出す（照合用） |
| `--card-log FILE` | B-CAS カードとのやり取りを書き出す（照合用。鍵が入るので人に渡さない） |

```bash
# NHK 総合（ch27 の例）を 30 秒、内蔵の B-CAS で外して録る
bin/recfriio --hdp --b25 27 30 nhk.ts

# 標準出力に流し続ける（Mirakurun などから）
bin/recfriio --hdp --b25 27 - -
```

ほかのプロセスが使っている UT100B は選びません（USB のインターフェースを取れないため）。  
`bin/recut100b …` は `bin/recfriio --hdp …` と同じです。

## C/C++ 版の recfriio との違い

- 対象は QRS-UT100B と MonsterTV U3S だけです（Friio・HDUS・HDP などは無い）。
- `--udp`・`--http`・`--sid`・`--lnb`・`--lockfile` はありません。
- UT100B のヌルパケットは、UT100B の TS フィルターで捨てています（C/C++ 版と同じ）。
- B25 の処理は arib25 をそのまま移しましたが、失敗したときの扱いを 2 つ変えています（`internal/b25/b25.go` の頭）。

## 試験

```bash
go test ./...
```

実機の無いところで回せる試験です。

- recfriio の制御転送の記録（`testdata/usb-trace-hdp-ch19.txt`）との照合
- ASIE5606 の USB 転送の DES を外す処理（実機のヌルパケット、昔の `decrypt.c` の出力）
- MULTI2（arib25 の出力）
- B25（作り物の放送とカード）
- 内蔵スロットの T=1（作り物の ASIE5606）
- pcscd との通信（作り物の pcscd）

実機での照合は `tools/arib25-compare/compare.sh` です。  
`--raw` と `--card-log` で取った TS とカードの記録を、arib25（C）と Go で外して比べます。

## MonsterTV U3S（`recfriio --u3s`、`recmtvu3s`）

SKNET MonsterTV U3S（`3275:9015`、地デジ・BS・CS）は `recfriio --u3s`（`recmtvu3s` と同じ）で受けます。  
C/C++ 版の recfriio の `--u3s`（`Mtvu3s`・`Tc90522`・`Fc0013`・`Qm1d1c0042`）を Go に移したものです。

```bash
bin/recfriio --u3s --stats 21 30 out.ts       # 地デジ
bin/recfriio --stats BS15_0 30 out.ts         # BS（BS・CS は U3S しか受けられないので --u3s は無くてもよい）
bin/recfriio --stats CS2 30 out.ts            # 110 度 CS
```

- USB は isochronous（EP `0x84`、altsetting 1、940 バイト × 16 フレーム / URB）で受けます（`internal/usbfs` の `StartIsoStream`）。
- EM28178 が USB 転送でかける AES-128-ECB は、`internal/mtvu3s` の `Descrambler` で外します。  
  `--no-descramble` を付けると外さずに書き出します（照合用。ペイロードは暗号がかかったまま）。
- 受け始めの、復調部が落ち着く前のパケット（TEI 付き。地デジで 1 秒弱）は捨てます（`Settler`）。  
  TEI の無いパケットが 5,000 個続いたら書き出し始めます。`--no-settle` で捨てずに書き出します。
- 衛星は、TMCC に載っている TSID の一覧と突き合わせて TS を選びます。  
  BS は、登録年のビット（`0x0e00`）を無視して当てます（`BS23_1` → `0x4971` など）。  
  CS は表（CS2 = `0x6020` など）から渡します。  
  一覧に無い TS（例: 2026-11-19 の `BS23_2`）を頼むとエラーで止まります。  
  recfriio は黙って別の TS（一覧の最初）を出していました。
- U3S には B-CAS のスロットが無いので、`--b25` はいつも PC/SC のカードリーダーを使います。
- `--stats` は、終わりに TS のヘッダーだけを数えた結果（同期・TEI・連続カウンター・PID）を書きます。ペイロードは見ないので、暗号がかかったままでも使えます。
- 地デジの C/N は Linux の tc90522 と同じ式です（recfriio の U3S の式とは値が違います）。
- 試験: `internal/mtvu3s/golden_test.go` が、チップの手順で I2C に送るバイト列を、recfriio の C++ のクラスで取った記録（`testdata/i2c-golden.txt`）と比べます。

2026-11-19 の実機（`--no-descramble`、暗号を外す前の TS のヘッダーで確かめた結果）:

| チャンネル | 長さ | パケット | 同期の乱れ | TEI | 連続カウンターの飛び | C/N |
|---|---|---|---|---|---|---|
| 地デジ ch21 | 5 秒 | 99,551 | 0 | 0 | 0 | 31.4 dB |
| BS15_0 | 5 秒 | 79,493 | 0 | 0 | 0 | 13.8 dB |
| CS2 | 5 秒 | 173,422 | 0 | 0 | 0 | 10.6 dB |
| 地デジ ch21（頭を捨てない） | 60 秒 | 1,195,083 | 0 | 6,369 | 69 | 31.1 dB |
| 地デジ ch21（頭を捨てる） | 60 秒 | 1,181,834 | 0 | 0 | 0 | — |

頭を捨てないときの TEI は全部、受け始めの 1 秒に固まっていました（ロックの直後で、復調部がまだ落ち着いていない）。  
recfriio の確かめも末尾だけを数えていた（`tail20k_tei`）ので、たぶん同じことが起きています。  
衛星の TS は、PAT の `transport_stream_id`（暗号のかかっていない 8〜9 バイト目）で確かめました（CS2 = `0x6020`、BS15_0 = `0x40f1`、BS01_0 = `0x4010`、BS23_1 = `0x4971`、BS23_3 = `0x4973`）。

AES を外したあと（2026-11-19、3 秒ずつ）:

| チャンネル | PAT の TSID | パケット | 同期の乱れ・TEI・連続カウンターの飛び | PAT の CRC-32 が合ったもの |
|---|---|---|---|---|
| 地デジ 21 | `0x7fe4` | 47,108 | 0 | 23 |
| BS15_0 | `0x40f1` | 47,665 | 0 | 30 |
| CS2 | `0x6020` | 103,978 | 0 | 30 |

`--b25`（外付けのリーダー ACR39U、地デジ 21 で 5 秒）: 87,075 パケット、乱れ 0、スクランブルの残り 0、PAT の CRC-32 が合ったもの 44。

## 資料

- `docs/QRS-UT100B.md`: UT100B の中身（ASV5211・ASIE5606・復調部・チューナー）、レジスタ、USB 転送の暗号について調べた結果
- `docs/SK-MTVU3S.md`: MonsterTV U3S の中身と、C++ 版で地デジ・BS・CS を通したときの記録
- `docs/modernization_design.md`: 書き直しの設計と、その後に分かったこと
- `docs/reference/`: 照合に使う記録（C++ 版が UT100B で送った制御転送など）
- `tools/`: 調べるときの道具（Python・シェル・C。ファームの取り出し、TS の解析、内蔵 B-CAS の確かめ）

## C/C++ 版からの流れ

- 元は recfriio r5（clworld ほか）と、STZ 版（stz2012・tslroom）の HDUS・HDP・FriioExpress の対応です。
- 過去の改変者の README（Shift_JIS の原文を UTF-8 にしたもの）は [`docs/original_readmes/`](docs/original_readmes/) にあります。
  - [`README.stz`](docs/original_readmes/README.stz): recfriio r5 + α（STZ 版）
  - [`README.HDUS`](docs/original_readmes/README.HDUS): HDUS 対応パッチ
  - [`README.EXPRESS`](docs/original_readmes/README.EXPRESS): FriioExpress 対応パッチ
  - [`README.arib25`](docs/original_readmes/README.arib25): 茂木 和洋 氏の arib25
- C/C++ 版の最後はタグ `cpp-final` です。Go 版の UT100B・U3S の手順は、そこからの移植です。

## ライセンス

作者の違う部分を合わせています。  
ファイルごとの持ち主は `LICENSE` と各ファイルの頭にあります。

- 初期化・選局の手順と値: recfriio（FUKAUMI Naoki、2 条項 BSD）と、その HDP・UT100B の対応（tslroom、stz2012）
- B25・MULTI2・B-CAS の命令: arib25（茂木 和洋）の移植
- MonsterTV U3S の部分（`internal/em28178`・`tc90522`・`fc0013`・`qm1d1c0042`・`mtvu3s`・`i2c`・`tscheck`）: waya0125（MIT。移植元の recfriio の U3S の部分も MIT）
- それ以外（`tools/`・`firmware/`・`docs/` を含む）: waya0125（MIT）
