package flicamera

/*
#include <stdint.h>

extern void imageReceived(void*, void*);

void imageAvailableWrapper(const uint8_t* image, void* ctx)
{
	imageReceived((void*)image, ctx);
}

*/
import "C"
