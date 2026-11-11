/*
 * SPDX-License-Identifier: MIT
 * Copyright (c) 2026 KASHIWAYA Rikuto
 *
 * arib25（C）で TS のスクランブルを外し、recut100b（Go）の結果と比べるための道具。
 * B-CAS カードの代わりに、recut100b --card-log で書いたやり取りの記録から答えを返す。
 *
 *   replay <arib25 の置き場> は compare.sh が組み立てる。
 *   ./replay card.log in.ts out.ts
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include "arib_std_b25.h"
#include "b_cas_card.h"

typedef struct { char *cmd; uint8_t *resp; int len; } exchange;
static exchange ex[4096];
static int nex;

static int unhex(const char *s, uint8_t *out) {
	int n = 0;
	while (s[0] && s[1] && s[0] != '\n') {
		unsigned v; sscanf(s, "%2x", &v); out[n++] = (uint8_t)v; s += 2;
	}
	return n;
}

static int load(const char *path) {
	FILE *f = fopen(path, "r");
	if (!f) return -1;
	char line[8192], cmd[8192] = "";
	while (fgets(line, sizeof line, f)) {
		line[strcspn(line, "\n")] = 0;
		if (line[0] == '>') strcpy(cmd, line + 2);
		else if (line[0] == '<' && nex < 4096) {
			ex[nex].cmd = strdup(cmd);
			ex[nex].resp = malloc(strlen(line) / 2);
			ex[nex].len = unhex(line + 2, ex[nex].resp);
			nex++;
		}
	}
	fclose(f);
	return 0;
}

static const exchange *find(const uint8_t *cmd, int len) {
	char hex[8192];
	for (int i = 0; i < len; i++) sprintf(hex + i * 2, "%02x", cmd[i]);
	hex[len * 2] = 0;
	for (int i = 0; i < nex; i++) if (strcmp(ex[i].cmd, hex) == 0) return &ex[i];
	return NULL;
}

static int get_init_status(void *bcas, B_CAS_INIT_STATUS *stat) {
	static const uint8_t init[] = { 0x90, 0x30, 0x00, 0x00, 0x00 };
	const exchange *e = find(init, sizeof init);
	if (!e || e->len < 57) return -1;
	const uint8_t *p = e->resp;
	memcpy(stat->system_key, p + 16, 32);
	memcpy(stat->init_cbc, p + 48, 8);
	stat->bcas_card_id = 0;
	stat->card_status = (p[2] << 8) | p[3];
	stat->ca_system_id = (p[6] << 8) | p[7];
	return 0;
}

static int get_id(void *bcas, B_CAS_ID *dst) { dst->data = NULL; dst->count = 0; return 0; }

static int proc_ecm(void *bcas, B_CAS_ECM_RESULT *dst, uint8_t *src, int len) {
	uint8_t cmd[300] = { 0x90, 0x34, 0x00, 0x00, (uint8_t)len };
	memcpy(cmd + 5, src, len);
	cmd[5 + len] = 0;
	const exchange *e = find(cmd, len + 6);
	if (!e || e->len < 25) { fprintf(stderr, "記録に無い ECM\n"); return -1; }
	memcpy(dst->scramble_key, e->resp + 6, 16);
	dst->return_code = (e->resp[4] << 8) | e->resp[5];
	return 0;
}

int main(int argc, char **argv) {
	if (argc != 4 || load(argv[1]) < 0) { fprintf(stderr, "usage: replay card.log in.ts out.ts\n"); return 2; }
	B_CAS_CARD card = { 0 };
	card.get_init_status = get_init_status;
	card.get_id = get_id;
	card.proc_ecm = proc_ecm;
	ARIB_STD_B25 *b25 = create_arib_std_b25();
	b25->set_multi2_round(b25, 4);
	b25->set_strip(b25, 0);
	b25->set_emm_proc(b25, 0);
	if (b25->set_b_cas_card(b25, &card) < 0) { fprintf(stderr, "set_b_cas_card\n"); return 1; }
	FILE *in = fopen(argv[2], "rb"), *out = fopen(argv[3], "wb");
	static uint8_t buf[65536];
	size_t n;
	int errors = 0;
	while ((n = fread(buf, 1, sizeof buf, in)) > 0) {
		ARIB_STD_B25_BUFFER sbuf = { buf, (int32_t)n }, dbuf;
		if (b25->put(b25, &sbuf) < 0) errors++;
		b25->get(b25, &dbuf);
		fwrite(dbuf.data, 1, dbuf.size, out);
	}
	ARIB_STD_B25_BUFFER dbuf;
	if (b25->flush(b25) < 0) errors++;
	b25->get(b25, &dbuf);
	fwrite(dbuf.data, 1, dbuf.size, out);
	fclose(out);
	fprintf(stderr, "arib25: errors %d\n", errors);
	return errors ? 1 : 0;
}
