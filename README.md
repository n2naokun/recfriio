# recut100b

Quixun QRS-UT100B（地上デジタル放送の USB チューナー）で受信し、TS を書き出すコマンドです。  
recfriio（C/C++）の UT100B の部分（`--hdp`）を Go で書き直しました。  
cgo・libusb・libpcsclite を使わず、Linux の usbfs と pcscd に直接話す 1 つのバイナリになります。

- チューナーの初期化・選局・C/N の読み取り
- UT100B の中の ASIE5606 がかける TS のスクランブル（DES）を外す
- B-CAS カードで ARIB STD-B25 のスクランブルを外す（`--b25`）。  
  カードは UT100B の内蔵スロットのものを使います（外付けのリーダーは `--pcsc`）。
- 付属 CD のドライバーからファームを取り出して送る（`ut100b-firmware`）

## 動作確認

| 機器 | 環境 | 結果 |
|---|---|---|
| Quixun QRS-UT100B | Ubuntu 26.04（kernel 7.0）、amd64 | 下の表のとおり |

確かめたのはこの 1 台だけです。  
同じ作りの MonsterTV HDUS・HDUC などは、実機が無いので対象にしていません。

| 確かめたこと | 結果 |
|---|---|
| 制御転送の並び | recfriio が ch19 を開いて閉じるまでに送る 234 回と、1 回ずつ同じ（`internal/ut100b` の試験） |
| 受信（ch19、60 秒） | 約 64.7 万パケット、TS の誤り・欠けなし |
| 内蔵 B-CAS で復号（`--b25`、60 秒） | 全パケットのスクランブルが外れ、誤り・欠けなし |
| arib25（C）との照合 | 同じ入力（B25 を通す前の TS、60 秒・約 120 MB）を arib25 と Go で外した結果が、バイト単位で同じ（`tools/arib25-compare`） |
| ファームの取り出し | 付属 CD の 2 つのローダーから、知っている 3 つのファームがハッシュどおりに取り出せる |

まだ確かめていないこと:
- `--pcsc`（外付けのカードリーダー）を実機で。  
  作り物の pcscd との試験は通っています。
- `ut100b-firmware load` と `--firmware` を実機で（抜き差しが要るため）。  
  送るリクエストは、recfriio の道具で実機を確かめたものと同じです。
- 30 分以上の長い受信。

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

引数の形は recfriio・recpt1 と同じです。

```bash
bin/recut100b [オプション] チャンネル 秒 出力先
```

| 引数 | 意味 |
|---|---|
| チャンネル | `13`〜`62`（UHF）、`C13`〜`C63`（CATV。`K13` とも書ける） |
| 秒 | 録る長さ。`-` なら止められる（SIGINT・SIGTERM）まで |
| 出力先 | 書き出すファイル。`-` なら標準出力 |

| オプション | 意味 |
|---|---|
| `--b25` | B-CAS カードでスクランブルを外す。カードは内蔵のスロットのもの |
| `--pcsc` | `--b25` で、外付けのカードリーダーを使う（pcscd の窓口は `PCSCLITE_CSOCK_NAME` で変えられる） |
| `--round N` | MULTI2 の段数（既定 4） |
| `--strip` | ヌルパケットを捨てる（UT100B はもともと出さない） |
| `--sync` | `--b25` で、鍵がそろうまでの失敗を数えない |
| `--device PATH` | 使う UT100B（`/dev/bus/usb/BBB/DDD`）。既定は空いているもの |
| `--firmware FILE` | ファームの無い UT100B しか無ければ、これを送ってから使う |
| `--raw FILE` | B25 を通す前の TS も書き出す（照合用） |
| `--card-log FILE` | B-CAS カードとのやり取りを書き出す（照合用。鍵が入るので人に渡さない） |

```bash
# NHK 総合（ch27 の例）を 30 秒、内蔵の B-CAS で外して録る
bin/recut100b --b25 27 30 nhk.ts

# 標準出力に流し続ける（Mirakurun などから）
bin/recut100b --b25 27 - -
```

ほかのプロセスが使っている UT100B は選びません（USB のインターフェースを取れないため）。  
環境変数 `RECUT100B_USB_TRACE=1` で、送る制御転送を全部、標準エラーに書きます。

## recfriio との違い

- 対象は QRS-UT100B だけです。
- `--udp`・`--http`・`--sid`・`--lnb`・`--lockfile` はありません。
- ヌルパケットは UT100B の TS フィルターで捨てています（recfriio と同じ）。
- B25 の処理は arib25 をそのまま移しましたが、失敗したときの扱いを 2 つ変えています（`internal/b25/b25.go` の頭）。

## 試験

```bash
go test ./...
```

実機の無いところで回せる試験です。

- recfriio の制御転送の記録（`testdata/usb-trace-hdp-ch19.txt`）との照合
- ASIE5606 のスクランブル外し（実機のヌルパケット、昔の `decrypt.c` の出力）
- MULTI2（arib25 の出力）
- B25（作り物の放送とカード）
- 内蔵スロットの T=1（作り物の ASIE5606）
- pcscd との通信（作り物の pcscd）

実機での照合は `tools/arib25-compare/compare.sh` です。  
`--raw` と `--card-log` で取った TS とカードの記録を、arib25（C）と Go で外して比べます。

## 資料

- `docs/QRS-UT100B.md`: UT100B の中身（ASV5211・ASIE5606・復調部・チューナー）、レジスタ、スクランブルについて調べた結果

## ライセンス

作者の違う部分を合わせています。  
ファイルごとの持ち主は `LICENSE` と各ファイルの頭にあります。

- 初期化・選局の手順と値: recfriio（FUKAUMI Naoki、2 条項 BSD）と、その HDP・UT100B の対応（tslroom、stz2012）
- B25・MULTI2・B-CAS の命令: arib25（茂木 和洋）の移植
- それ以外: KASHIWAYA Rikuto（MIT）
