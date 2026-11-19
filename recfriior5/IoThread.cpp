// SPDX-License-Identifier: MIT
// Copyright (c) 2008 clworld
// Copyright (c) 2026 waya0125

// $Id: IoThread.cpp 6314 2008-11-04 02:17:00Z clworld $
// 非同期リクエスト発行スレッド
// PushIoThread, PopIoThread

#include <inttypes.h>
#include <errno.h>
#include <err.h>
#include <string.h>
#include <unistd.h>

#include <sys/ioctl.h>

#include <algorithm>
#include <cstddef>
#include <iostream>

#include "usbops.hpp"
#include "RingBuf.hpp"
#include "IoThread.hpp"

using namespace std;

/**
 * 送信したURBをキャンセルする。
 */
static void discard_async_request(AsyncRequest *r) {
	int ret = usb_discardurb(r->fd, r->urbPtr());
	// EINVALは該当URBがkernel側のキューにない場合に返されるので無視で問題ない。
	if (ret < 0 && errno != EINVAL) {
		if (errno == EINVAL) {
			std::cerr << "can't discard urb (at timeout, EINVAL) ignored." << std::endl;
		} else {
			err(1, "can't discard urb (at timeout)");
		}
	}
}

/**
 * 非同期URBリクエスト発行スレッド
 * スレッド処理本体
 */
void
PushIoThread::operator()() {
	while(!is_cancelled) {
		// リクエスト待ちが一杯であればREQUEST_POLLING_WAIT(ms)待ってやりなおし。
		if (requestReserveNum <= buf->getWaitCount()) {
			// リクエスト待ちが一杯です。
			usleep(requestPollingWait * 1000);
			continue;
		
		}
		// リクエスト待ちに空きがある。
		AsyncRequest *req          = buf->getPushPtr(REQUEST_TIMEOUT_TS, discard_async_request);
		
		memset(req->urb_storage, '\0', sizeof(req->urb_storage));
		memset(req->buf, '\0', TSDATASIZE);

		usbdevfs_urb *urb = req->urbPtr();
		if (isoPacketSize > 0 && isoPacketsPerUrb > 0) {
			urb->type              = USBDEVFS_URB_TYPE_ISO;
			urb->flags             = USBDEVFS_URB_ISO_ASAP;
			urb->buffer_length     = static_cast<int>(isoPacketSize * isoPacketsPerUrb);
			urb->number_of_packets = static_cast<int>(isoPacketsPerUrb);
			for (unsigned int i = 0; i < isoPacketsPerUrb; i++) {
				urb->iso_frame_desc[i].length = isoPacketSize;
			}
		} else {
			urb->type              = USBDEVFS_URB_TYPE_BULK;
			urb->buffer_length     = TSDATASIZE;
			urb->number_of_packets = 0;
		}
		urb->endpoint          = endpoint;
		urb->status            = 0;
		urb->buffer            = req->buf;
		urb->actual_length     = 0;
		urb->signr             = 0;
		urb->usercontext       = req;
		req->fd = fd;
		
		int r = usb_submiturb(fd, urb);
		if (r < 0) {
			err(1, "can't send urb");
		}
	}
}

/**
 * 非同期URBリクエスト受信スレッド
 * コンストラクタ
 * @param fd USBのリクエストを受け取るファイルディスクリプタ
 * @param buf 非同期リクエストのデータを入れるリングバッファ
 */
PopIoThread::PopIoThread(int fd, RingBuf<AsyncRequest> *buf) : fd(fd), buf(buf), is_cancelled(false) {
	int pipefds[2];
	int r = pipe(pipefds);
	if (r < 0) {
		err(1, "can't create pipe.");
	}
	
	event_fd_pop  = pipefds[0];
	event_fd_push = pipefds[1];
}

/**
 * 非同期URBリクエスト受信スレッド
 * デストラクタ
 */
PopIoThread::~PopIoThread() {
	if (close(event_fd_push) < 0) {
		err(1, "can't clsoe event_fd_push.");
	}
	if (close(event_fd_pop) < 0) {
		err(1, "can't clsoe event_fd_pop.");
	}
}
	
/**
 * 非同期URBリクエスト受信スレッド
 * スレッド処理本体
 */
void
PopIoThread::operator()() {
	fd_set readfds, writefds;
	
	while(!is_cancelled) {
		FD_ZERO(&readfds);
		FD_SET(event_fd_pop, &readfds);
		FD_SET(fd, &readfds);

		FD_ZERO(&writefds);
		FD_SET(fd, &writefds);
		
		int maxfd = std::max(fd, event_fd_pop);
		struct timeval tv = { 0, 5000 };
		int r = select(maxfd+1, &readfds, &writefds, NULL, &tv);
		if (r < 0) {
			if (errno == EINTR) continue;
			err(1, "select failed.");
		}
		
		if (FD_ISSET(event_fd_pop, &readfds)) {
			// cancelから起こされた？
			uint8_t dummybuf[1];
			int r = read(event_fd_pop, dummybuf, 1);
			if (r < 0) {
				err(1, "can't read from event_fd.");
			}
		}
		
		if (FD_ISSET(fd, &readfds) || FD_ISSET(fd, &writefds)) {
			// usbfsから何か来た
			reapUrbs();
		}
	}
}

/**
 * 非同期URBリクエスト受信スレッド
 * スレッドを停止させる
 */
void
PopIoThread::cancel() {
	is_cancelled = true;
	// selectを起こす為に書き込む
	int r = write(event_fd_push, "n", 1);
	if (r < 0) {
		err(1, "can't write to event_fd.");
	}
}
	
/**
 * 非同期URBリクエスト受信スレッド
 * usbfsから完了URBを読み込む
 */
void
PopIoThread::reapUrbs() {
	usbdevfs_urb *urb;
	while (usb_reapurb_ndelay(fd, &urb) >= 0) {
		// 読み込めた
		AsyncRequest *req = reinterpret_cast<AsyncRequest *>(urb->usercontext);
		buf->setReady(req);
	}
}
