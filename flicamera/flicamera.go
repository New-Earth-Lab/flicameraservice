package flicamera

/*
#cgo LDFLAGS: -L/opt/FirstLightImaging/FliSdk/lib/release -lFliSdk
#cgo CFLAGS: -I/opt/FirstLightImaging/FliSdk/include

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

extern void imageAvailableWrapper(const uint8_t* image, void* ctx);

#include "FliSdk_C_V2.h"
*/
import "C"
import (
	"fmt"
	"log"
	"runtime/cgo"
	"strings"
	"unsafe"

	"github.com/lirm/aeron-go/aeron"
	"github.com/lirm/aeron-go/aeron/atomic"
)

type FLICamera struct {
	context           C.FliContext
	callbackHandle    C.callbackHandler
	width             int
	height            int
	bytesPerPixel     int
	publication       *aeron.Publication
	pixelBuffer       *atomic.Buffer
	pixelBufferLength int
	headerBuffer      *atomic.Buffer
	handle            cgo.Handle
}

const (
	RingBufferNumImages = 4
)

func NewFliCamera(publication *aeron.Publication) (*FLICamera, error) {
	// Get the configuration for the camera
	// Grabber name
	// Camera name
	// ROI - cropping mode
	// Gain

	const (
		textSize = 512
	)

	var ok C.bool

	fliContext := C.FliSdk_init_V2()
	defer func(ok *C.bool) {
		if !*ok {
			C.FliSdk_exit_V2(fliContext)
		}
	}(&ok)

	text := (*C.char)(C.malloc(textSize))
	defer C.free(unsafe.Pointer(text))

	// Get list of grabbers
	C.FliSdk_detectGrabbers_V2(fliContext, text, textSize)
	grabberStrings := strings.Split(C.GoString(text), ";")
	if len(grabberStrings) == 0 {
		return nil, fmt.Errorf("No grabbers found")
	}

	// Get list of cameras
	C.FliSdk_detectCameras_V2(fliContext, text, textSize)
	cameraStrings := strings.Split(C.GoString(text), ";")
	if len(cameraStrings) == 0 {
		return nil, fmt.Errorf("No cameras found")
	}

	// Set the grabber to the configured model if found
	grabberName := C.CString(grabberStrings[0])
	defer C.free(unsafe.Pointer(grabberName))

	// Set the camera to the configured model if found
	cameraName := C.CString(cameraStrings[0])
	defer C.free(unsafe.Pointer(cameraName))

	if ok = C.FliSdk_setGrabber_V2(fliContext, grabberName); !ok {
		return nil, fmt.Errorf("Unable to set grabber: %s", grabberStrings[0])
	}

	if ok = C.FliSdk_setCamera_V2(fliContext, cameraName); !ok {
		return nil, fmt.Errorf("Unable to set camera: %s", cameraStrings[0])
	}

	C.FliSdk_setMode_V2(fliContext, C.Full)

	if ok = C.FliSdk_update_V2(fliContext); !ok {
		return nil, fmt.Errorf("Unable update SDK")
	}

	// Set sensor cropping
	croppingData := C.CroppingData_C{
		col1: 0,
		col2: 639,
		row1: 0,
		row2: 511,
	}
	if ok = C.FliSdk_isCroppingDataValid_V2(fliContext, croppingData); ok {
		if ok = C.FliSdk_setCroppingState_V2(fliContext, true, croppingData); !ok {
			return nil, fmt.Errorf("Unable to set cropping state")
		}
	} else {
		return nil, fmt.Errorf("Cropping data invalid")
	}

	C.FliSdk_enableRingBuffer_V2(fliContext, true)
	C.FliSdk_setBufferSizeInImages_V2(fliContext, RingBufferNumImages)
	C.FliSdk_setNbImagesPerBuffer_V2(fliContext, 1)

	// Get image dimensions for buffer size
	var width, height C.ushort
	C.FliSdk_getCurrentImageDimension_V2(fliContext, &width, &height)

	var bytesPerPixel int
	if C.FliSdk_isMono8Pixel_V2(fliContext) == true {
		bytesPerPixel = 1
	} else {
		bytesPerPixel = 2
	}

	context := FLICamera{
		context:           fliContext,
		pixelBuffer:       new(atomic.Buffer),
		pixelBufferLength: int(width) * int(height) * bytesPerPixel,
		bytesPerPixel:     bytesPerPixel,
		width:             int(width),
		height:            int(width),
		publication:       publication,
		headerBuffer:      atomic.MakeBuffer(make([]byte, 256)),
	}

	context.handle = cgo.NewHandle(context)
	context.callbackHandle = C.FliSdk_addCallbackNewImage_V2(fliContext,
		(C.newImageAvailableCallBack)(C.imageAvailableWrapper), 0, true,
		unsafe.Pointer(&context.handle))

	return &context, nil
}

func (f *FLICamera) StartCamera() error {
	if C.FliSdk_start_V2(f.context) == false {
		return fmt.Errorf("Unable to start camera")
	}

	return nil
}

func (f *FLICamera) StopCamera() error {
	if C.FliSdk_stop_V2(f.context) == false {
		return fmt.Errorf("Unable to stop camera")
	}

	return nil
}

func (f *FLICamera) Shutdown() error {
	C.FliSdk_exit_V2(f.context)
	f.handle.Delete()

	return nil
}

// type PixelBufferHeader struct {
// 	flyweight.FWBase

// 	Timestamp    flyweight.Int64Field
// 	Width        flyweight.Int32Field
// 	Height       flyweight.Int32Field
// 	BitsPerPixel flyweight.Int32Field
// }

// func (m *PixelBufferHeader) Wrap(buf *atomic.Buffer, offset int) flyweight.Flyweight {
// 	pos := offset
// 	pos += m.ClientID.Wrap(buf, pos)
// 	pos += m.CorrelationID.Wrap(buf, pos)

// 	m.SetSize(pos - offset)
// 	return m
// }

//export imageReceived
//go:nocheckptr go:nosplit
func imageReceived(image unsafe.Pointer, ctx unsafe.Pointer) {
	context := (*(*cgo.Handle)(ctx)).Value().(FLICamera)

	// context.headerBuffer.Wrap()

	// Wrap pixels
	context.pixelBuffer.Wrap(image, int32(context.pixelBufferLength))

	var ret int64
	for ok := true; ok; ok = (ret == aeron.BackPressured || ret == aeron.AdminAction) {
		ret = context.publication.Offer2(context.headerBuffer, 0,
			context.headerBuffer.Capacity(), context.pixelBuffer, 0,
			context.pixelBuffer.Capacity(), nil)
		switch ret {
		case aeron.NotConnected:
			// log.Printf("not connected yet")
		case aeron.BackPressured:
			// log.Printf("back pressured")
		case aeron.AdminAction:
			// log.Printf("admin action")
		case aeron.PublicationClosed:
			log.Printf("publication closed")
		default:
			if ret < 0 {
				log.Printf("Unrecognized code: %d", ret)
			}
		}
	}
}
