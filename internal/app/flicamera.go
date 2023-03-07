package app

/*
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

extern void imageAvailableWrapper(const uint8_t* image, void* ctx);
*/
import "C"
import (
	"log"
	"runtime/cgo"
	"unsafe"

	"github.com/New-Earth-Lab/flisdk-go/flisdk"
	"github.com/lirm/aeron-go/aeron"
	"github.com/lirm/aeron-go/aeron/atomic"
)

type FLICamera struct {
	sdk               *flisdk.FliSdk
	callbackHandler   flisdk.CallbackHandler
	width             uint
	height            uint
	bytesPerPixel     uint
	publication       *aeron.Publication
	pixelBuffer       *atomic.Buffer
	pixelBufferLength uint
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

	var err error
	sdk, err := flisdk.Init()
	if err != nil {
		return nil, err
	}
	defer func(e *error) {
		if *e != nil {
			sdk.Exit()
		}
	}(&err)

	// Get list of grabbers
	grabberStrings, err := sdk.DetectGrabbers()
	if err != nil {
		return nil, err
	}

	// Get list of cameras
	cameraStrings, err := sdk.DetectCameras()
	if err != nil {
		return nil, err
	}

	// Set the grabber to the configured model if found
	err = sdk.SetGrabber(grabberStrings[0])
	if err != nil {
		return nil, err
	}

	// Set the camera to the configured model if found
	err = sdk.SetCamera(cameraStrings[0])
	if err != nil {
		return nil, err
	}

	err = sdk.SetMode(flisdk.Mode_Full)
	if err != nil {
		return nil, err
	}

	err = sdk.Update()
	if err != nil {
		return nil, err
	}

	// Set sensor cropping
	croppingData := flisdk.CroppingData{
		Col1:    0,
		Col2:    639,
		Row1:    0,
		Row2:    511,
		Enabled: true,
	}

	err = sdk.SetCroppingState(croppingData)
	if err != nil {
		return nil, err
	}

	// Enable the ring buffer to shrink it
	sdk.EnableRingBuffer(true)
	sdk.SetBufferSizeInImages(RingBufferNumImages)

	// Disable the ring buffer
	sdk.EnableRingBuffer(false)
	sdk.SetNumberImagesPerBuffer(1)

	// Get image dimensions for buffer size
	width, height := sdk.GetCurrentImageDimension()

	cam := FLICamera{
		sdk:               sdk,
		pixelBuffer:       new(atomic.Buffer),
		pixelBufferLength: sdk.GetImageSizeInBytes(),
		bytesPerPixel:     sdk.GetBytesPerPixel(),
		width:             uint(width),
		height:            uint(height),
		publication:       publication,
		headerBuffer:      atomic.MakeBuffer(make([]byte, 256)),
	}

	cam.callbackHandler = sdk.AddCallbackNewImage(
		(flisdk.NewImageAvailableCallBack)(C.imageAvailableWrapper),
		0, true, unsafe.Pointer(&cam))

	return &cam, nil
}

func (f *FLICamera) StartCamera() error {
	return f.sdk.Start()
}

func (f *FLICamera) StopCamera() error {
	return f.sdk.Stop()
}

func (f *FLICamera) Shutdown() error {
	err := f.sdk.Stop()
	if err != nil {
		return err
	}

	f.sdk.RemoveCallbackNewImage(f.callbackHandler)
	f.sdk.Exit()

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
	cam := (*FLICamera)((*(*cgo.Handle)(ctx)).Value().(unsafe.Pointer))

	// context.headerBuffer.Wrap()

	// Wrap pixels
	cam.pixelBuffer.Wrap(image, int32(cam.pixelBufferLength))

	var ret int64
	for ok := true; ok; ok = (ret == aeron.BackPressured || ret == aeron.AdminAction) {
		// ret = context.publication.Offer2(context.headerBuffer, 0,
		// 	context.headerBuffer.Capacity(), context.pixelBuffer, 0,
		// 	context.pixelBuffer.Capacity(), nil)
		ret = cam.publication.Offer(cam.pixelBuffer, 0, cam.pixelBuffer.Capacity(), nil)
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
