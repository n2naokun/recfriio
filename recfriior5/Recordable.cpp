// $Id: Recordable.cpp 5663 2008-09-15 17:53:59Z clworld $
// 

#include <inttypes.h>
#include <stdint.h>
#include "FriioWhiteWrapper.hpp"
#include "FriioBlack.hpp"
#ifdef HDUS
#include "Hdus.hpp"
#include "Hdp.hpp"
#include "Mtvu3s.hpp"
#endif

const std::string tunerTypeName[TUNER_TYPES_LEN] = {
	"Friio(White)",
	"Friio(Black)",
#ifdef HDUS
	"HDUS",
	"HDP",
	"MonsterTV U3S",
#endif
};

extern "C" Recordable*
createRecordable(TunerType type) {
	switch(type) {
		case TUNER_FRIIO_WHITE:
			return new FriioWhiteWrapper();
			break;
		case TUNER_FRIIO_BLACK:
			return new FriioBlack();
			break;
#ifdef HDUS
		case TUNER_HDUS:
			return new Hdus();
			break;
		case TUNER_HDP:
			return new Hdp();
			break;
		case TUNER_MTVU3S:
			return new Mtvu3s();
			break;
#endif
		default:
			return NULL;
	}
}
