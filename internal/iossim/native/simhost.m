// simhost: one iOS Simulator for AgentBox's ios-helper (internal/iossim),
// which compiles it on the Mac with xcrun clang and runs one per simulator in
// use:
//
//   DEVELOPER_DIR=<Xcode's Developer directory> simhost <udid>
//
// It reads ops, one JSON object per line, on stdin:
//
//   {"op":"stream","on":true}       start (or, without "on", stop) the screen
//   {"op":"keyframe"}               send a keyframe next
//   {"op":"touch","phase":"down","x":0.5,"y":0.5}   x and y: fractions of the screen
//   {"op":"key","usage":4,"down":true}              a USB HID keyboard usage
//   {"op":"button","button":"home","down":true}     home, lock, siri, volumeUp, volumeDown
//
// and writes frames on stdout: a big-endian uint32 length (of what follows),
// a kind byte, and a payload. 1: the stream's config, JSON, with its codec
// string and its AVCDecoderConfigurationRecord (description, base64), before
// the first keyframe and whenever they change. 2: an H.264 access unit, as a
// flags byte (1: keyframe), a big-endian uint64 timestamp (µs since 1970),
// and its NAL units each after a 4-byte length (AVCC), as VideoToolbox makes
// them: the shapes of internal/devicestream's wire format, which the helper
// passes on. 3: an event, JSON: {"screen":{…}}, {"input":…} or {"error":…}.
//
// The screen is CoreSimulator's framebuffer, an IOSurface, encoded on the
// Mac's hardware H.264 encoder (software only when it has none) with no
// frame reordering, so no B-frames: each frame decodes as it arrives. Input
// goes to dtuhidd, the simulator's HID service since Xcode 27 (CoreSimulator
// 1155.4), or as SimulatorKit's Indigo messages before it. Both are private
// interfaces; their shapes come from facebook/idb's (MIT) notes on them,
// and are looked up at run time, so a change in them is an error here, not a
// crash or a build failure.

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <IOSurface/IOSurfaceRef.h>
#import <VideoToolbox/VideoToolbox.h>
#import <dlfcn.h>
#import <errno.h>
#import <mach/mach.h>
#import <mach/mach_time.h>
#import <objc/message.h>
#import <objc/runtime.h>
#import <signal.h>
#import <stdatomic.h>
#import <unistd.h>
#import <xpc/xpc.h>

#pragma mark - CoreSimulator and SimulatorKit, as far as simhost uses them

@protocol ABSimServiceContext <NSObject>
+ (id)sharedServiceContextForDeveloperDir:(NSString *)dir error:(NSError **)error;
- (id)defaultDeviceSetWithError:(NSError **)error;
@end

@protocol ABSimDeviceSet <NSObject>
- (NSDictionary *)devicesByUDID;
@end

@protocol ABSimDevice <NSObject>
- (id)io;
- (mach_port_t)lookup:(NSString *)name error:(NSError **)error;
@end

@protocol ABSimDeviceIOClient <NSObject>
- (NSArray *)ioPorts;
@end

@protocol ABSimDeviceIOPort <NSObject>
- (id)descriptor;
@end

@protocol ABSimDisplay <NSObject>
- (id)framebufferSurface;
- (id)ioSurface;
- (void)registerCallbackWithUUID:(NSUUID *)uuid damageRectanglesCallback:(void (^)(NSArray *))callback;
- (void)unregisterDamageRectanglesCallbackWithUUID:(NSUUID *)uuid;
@end

@protocol ABSimDisplayState <NSObject>
- (unsigned short)displayClass;
@end

@protocol ABLegacyHIDClient <NSObject>
- (instancetype)initWithDevice:(id)device error:(NSError **)error;
- (void)sendWithMessage:(void *)message
           freeWhenDone:(BOOL)freeWhenDone
        completionQueue:(dispatch_queue_t)queue
             completion:(void (^)(NSError *))completion;
@end

// SimDevice's state, a number, and a display descriptor's, an object: the
// same selector, so both are sent untyped.
static unsigned long long deviceState(id device) {
    return ((unsigned long long (*)(id, SEL))objc_msgSend)(device, sel_registerName("state"));
}

static id descriptorState(id descriptor) {
    return ((id (*)(id, SEL))objc_msgSend)(descriptor, sel_registerName("state"));
}

static const unsigned long long SimDeviceStateBooted = 3;

#pragma mark - Indigo messages (SimulatorKit, before Xcode 27)

#pragma pack(push, 4)
typedef struct {
    unsigned int field1;
    unsigned int field2;
    unsigned int eventMask;
    double xRatio;
    double yRatio;
    double field6;
    double field7;
    double field8;
    unsigned int range;
    unsigned int touch;
    unsigned int field11;
    unsigned int field12;
    unsigned int field13;
    double field14;
    double field15;
    double field16;
    double field17;
    double field18;
} IndigoTouch;

typedef struct {
    double field1, field2, field3, field4;
} IndigoQuad;

typedef union {
    IndigoTouch touch;
    IndigoQuad gameController[4]; // the largest member: the union's size
} IndigoEvent;

typedef struct {
    unsigned int eventKind;
    unsigned long long timestamp;
    unsigned int field3;
    IndigoEvent event;
} IndigoPayload;

typedef struct {
    mach_msg_header_t header;
    unsigned int innerSize;
    unsigned char eventType;
    IndigoPayload payload;
} IndigoMessage;
#pragma pack(pop)

_Static_assert(sizeof(IndigoPayload) == 0x90, "IndigoPayload is 0x90 bytes");
_Static_assert(offsetof(IndigoMessage, payload) == 0x20, "IndigoMessage's payload is at 0x20");

typedef IndigoMessage *(*IndigoButtonFn)(int32_t source, int32_t direction, int32_t target);
typedef IndigoMessage *(*IndigoKeyboardFn)(int32_t usage, int32_t direction);
typedef IndigoMessage *(*IndigoHIDArbitraryFn)(int32_t target, uint32_t page, uint32_t usage, int32_t direction);
typedef IndigoMessage *(*IndigoMouseFn)(CGPoint *point, CGPoint *point2, uint32_t target, unsigned long eventType, CGSize size, uint32_t edge);

#pragma mark - Output

enum { KindConfig = 1, KindFrame = 2, KindEvent = 3 };

static dispatch_queue_t outQueue;     // stdout, in order
static dispatch_queue_t captureQueue; // the screen and its encoder
static dispatch_queue_t hidQueue;     // input

static void writeAll(const void *data, size_t n) {
    const uint8_t *p = data;
    while (n > 0) {
        ssize_t w = write(STDOUT_FILENO, p, n);
        if (w < 0 && errno == EINTR) {
            continue;
        }
        if (w <= 0) {
            exit(0); // the helper has gone
        }
        p += w;
        n -= (size_t)w;
    }
}

static void emit(uint8_t kind, NSData *payload) {
    dispatch_async(outQueue, ^{
        uint8_t head[5];
        uint32_t n = (uint32_t)payload.length + 1;
        head[0] = (uint8_t)(n >> 24);
        head[1] = (uint8_t)(n >> 16);
        head[2] = (uint8_t)(n >> 8);
        head[3] = (uint8_t)n;
        head[4] = kind;
        writeAll(head, sizeof head);
        writeAll(payload.bytes, payload.length);
    });
}

static void emitJSON(uint8_t kind, NSDictionary *object) {
    NSData *data = [NSJSONSerialization dataWithJSONObject:object options:0 error:nil];
    if (data) {
        emit(kind, data);
    }
}

static void report(NSString *error) {
    fprintf(stderr, "%s\n", error.UTF8String);
    emitJSON(KindEvent, @{@"error" : error});
}

#pragma mark - The simulator

static NSString *developerDir;
static id device;
static void *simulatorKit;

static BOOL loadFrameworks(void) {
    NSArray *coreSimulator = @[
        @"/Library/Developer/PrivateFrameworks/CoreSimulator.framework/CoreSimulator",
        [developerDir stringByAppendingPathComponent:@"Library/PrivateFrameworks/CoreSimulator.framework/CoreSimulator"],
    ];
    BOOL loaded = NO;
    for (NSString *path in coreSimulator) {
        if (dlopen(path.fileSystemRepresentation, RTLD_NOW | RTLD_GLOBAL)) {
            loaded = YES;
            break;
        }
    }
    if (!loaded) {
        report([NSString stringWithFormat:@"CoreSimulator didn't load: %s", dlerror()]);
        return NO;
    }
    // Only Indigo input needs it; dtuhidd doesn't.
    NSString *kit = [developerDir stringByAppendingPathComponent:@"Library/PrivateFrameworks/SimulatorKit.framework/SimulatorKit"];
    simulatorKit = dlopen(kit.fileSystemRepresentation, RTLD_NOW | RTLD_GLOBAL);
    return YES;
}

static id findDevice(NSString *udid) {
    Class contextClass = NSClassFromString(@"SimServiceContext");
    if (!contextClass) {
        report(@"CoreSimulator has no SimServiceContext");
        return nil;
    }
    NSError *error = nil;
    id context = [(Class<ABSimServiceContext>)contextClass sharedServiceContextForDeveloperDir:developerDir error:&error];
    id set = context ? [(id<ABSimServiceContext>)context defaultDeviceSetWithError:&error] : nil;
    if (!set) {
        report([NSString stringWithFormat:@"CoreSimulator's devices: %@", error.localizedDescription ?: @"unavailable"]);
        return nil;
    }
    NSUUID *uuid = [[NSUUID alloc] initWithUUIDString:udid];
    id found = uuid ? [(id<ABSimDeviceSet>)set devicesByUDID][uuid] : nil;
    if (!found) {
        report([NSString stringWithFormat:@"no simulator %@", udid]);
    }
    return found;
}

#pragma mark - The screen

static id display;                 // the main display's descriptor
static IOSurfaceRef surface;        // its framebuffer, retained
static NSUUID *damageToken;
static atomic_bool damaged;
static size_t screenWidth, screenHeight;

// findDisplay is the IO port whose descriptor renders to an IOSurface and is
// the main display (display class 0), or else the first that renders.
static id findDisplay(void) {
    id io = nil;
    @try {
        io = [(id<ABSimDevice>)device io];
    } @catch (NSException *e) {
        return nil;
    }
    id fallback = nil;
    NSArray *ports = nil;
    @try {
        ports = [(id<ABSimDeviceIOClient>)io ioPorts];
    } @catch (NSException *e) {
        return nil;
    }
    for (id port in ports) {
        id descriptor = nil;
        @try {
            descriptor = [(id<ABSimDeviceIOPort>)port descriptor];
        } @catch (NSException *e) {
            continue;
        }
        if (![descriptor respondsToSelector:@selector(framebufferSurface)] && ![descriptor respondsToSelector:@selector(ioSurface)]) {
            continue;
        }
        if ([descriptor respondsToSelector:sel_registerName("state")]) {
            id state = descriptorState(descriptor);
            if ([state respondsToSelector:@selector(displayClass)] && [(id<ABSimDisplayState>)state displayClass] == 0) {
                return descriptor;
            }
        }
        if (!fallback) {
            fallback = descriptor;
        }
    }
    return fallback;
}

// surfaceOf is a display's framebuffer. The descriptor is a proxy that may
// answer either selector and still have nothing behind it, so both are tried.
static IOSurfaceRef surfaceOf(id d) {
    id s = nil;
    @try {
        s = [(id<ABSimDisplay>)d framebufferSurface];
    } @catch (NSException *e) {
    }
    if (!s) {
        @try {
            s = [(id<ABSimDisplay>)d ioSurface];
        } @catch (NSException *e) {
        }
    }
    if (!s || CFGetTypeID((__bridge CFTypeRef)s) != IOSurfaceGetTypeID()) {
        return NULL;
    }
    return (__bridge IOSurfaceRef)s;
}

// refreshSurface finds the screen, which changes when the simulator boots
// again or rotates, and says when its size does.
static void refreshSurface(void) {
    if (deviceState(device) != SimDeviceStateBooted) {
        display = nil;
    } else if (!display) {
        display = findDisplay();
        if (display && [display respondsToSelector:@selector(registerCallbackWithUUID:damageRectanglesCallback:)]) {
            damageToken = [NSUUID UUID];
            @try {
                [(id<ABSimDisplay>)display registerCallbackWithUUID:damageToken
                                           damageRectanglesCallback:^(NSArray *rects) {
                                             atomic_store(&damaged, true);
                                           }];
            } @catch (NSException *e) {
                damageToken = nil;
            }
        }
    }
    IOSurfaceRef s = display ? surfaceOf(display) : NULL;
    if (s == surface) {
        return;
    }
    if (surface) {
        CFRelease(surface);
    }
    surface = s ? (IOSurfaceRef)CFRetain(s) : NULL;
    if (s && (IOSurfaceGetWidth(s) != screenWidth || IOSurfaceGetHeight(s) != screenHeight)) {
        screenWidth = IOSurfaceGetWidth(s);
        screenHeight = IOSurfaceGetHeight(s);
        emitJSON(KindEvent, @{@"screen" : @{@"width" : @(screenWidth), @"height" : @(screenHeight)}});
    }
}

#pragma mark - The encoder

static const int maxSide = 1920;

static VTCompressionSessionRef session;
static VTPixelTransferSessionRef transfer;
static CVPixelBufferPoolRef pool;
static size_t sourceWidth, sourceHeight;
static int encodeWidth, encodeHeight;
static BOOL hardware;
static NSString *codec;
static BOOL forceKey;
static uint64_t started;            // mach time the stream started
static double startedWall;          // and the time of day then, in seconds
static uint64_t lastEncoded;
static mach_timebase_info_data_t timebase;
static dispatch_source_t timer;

static uint64_t micros(uint64_t machTime) {
    return machTime * timebase.numer / timebase.denom / 1000;
}

// avcCOf is a format's AVCDecoderConfigurationRecord: VideoToolbox's own
// avcC atom, or one made from its parameter sets.
static NSData *avcCOf(CMFormatDescriptionRef format) {
    CFDictionaryRef atoms = (CFDictionaryRef)CMFormatDescriptionGetExtension(format, kCMFormatDescriptionExtension_SampleDescriptionExtensionAtoms);
    if (atoms && CFGetTypeID(atoms) == CFDictionaryGetTypeID()) {
        CFDataRef avcC = (CFDataRef)CFDictionaryGetValue(atoms, CFSTR("avcC"));
        if (avcC && CFGetTypeID(avcC) == CFDataGetTypeID()) {
            return (__bridge NSData *)avcC;
        }
    }
    const uint8_t *sps = NULL, *pps = NULL;
    size_t spsLength = 0, ppsLength = 0;
    if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, 0, &sps, &spsLength, NULL, NULL) != noErr ||
        CMVideoFormatDescriptionGetH264ParameterSetAtIndex(format, 1, &pps, &ppsLength, NULL, NULL) != noErr || spsLength < 4) {
        return nil;
    }
    NSMutableData *d = [NSMutableData data];
    uint8_t head[] = {1, sps[1], sps[2], sps[3], 0xFF, 0xE1, (uint8_t)(spsLength >> 8), (uint8_t)spsLength};
    [d appendBytes:head length:sizeof head];
    [d appendBytes:sps length:spsLength];
    uint8_t mid[] = {1, (uint8_t)(ppsLength >> 8), (uint8_t)ppsLength};
    [d appendBytes:mid length:sizeof mid];
    [d appendBytes:pps length:ppsLength];
    return d;
}

static NSData *avcCSent; // the description last in a config

static void encoded(void *ref, void *frameRef, OSStatus status, VTEncodeInfoFlags flags, CMSampleBufferRef sample) {
    if (status != noErr || !sample || !CMSampleBufferDataIsReady(sample)) {
        return;
    }
    BOOL key = YES;
    CFArrayRef attachments = CMSampleBufferGetSampleAttachmentsArray(sample, false);
    if (attachments && CFArrayGetCount(attachments) > 0) {
        CFDictionaryRef a = CFArrayGetValueAtIndex(attachments, 0);
        key = CFDictionaryGetValue(a, kCMSampleAttachmentKey_NotSync) != kCFBooleanTrue;
    }
    if (key) {
        NSData *avcC = avcCOf(CMSampleBufferGetFormatDescription(sample));
        if (avcC.length >= 4 && ![avcC isEqualToData:avcCSent]) {
            const uint8_t *c = avcC.bytes;
            avcCSent = [avcC copy];
            codec = [NSString stringWithFormat:@"avc1.%02x%02x%02x", c[1], c[2], c[3]];
            emitJSON(KindConfig, @{
                @"codec" : codec,
                @"width" : @(encodeWidth),
                @"height" : @(encodeHeight),
                @"description" : [avcCSent base64EncodedStringWithOptions:0],
                @"hardware" : @(hardware),
            });
        }
    }
    CMBlockBufferRef block = CMSampleBufferGetDataBuffer(sample);
    size_t total = CMBlockBufferGetDataLength(block);
    NSMutableData *out = [NSMutableData dataWithLength:9 + total];
    uint8_t *p = out.mutableBytes;
    p[0] = key ? 1 : 0;
    uint64_t at = (uint64_t)((startedWall + CMTimeGetSeconds(CMSampleBufferGetPresentationTimeStamp(sample))) * 1e6);
    for (int i = 0; i < 8; i++) {
        p[1 + i] = (uint8_t)(at >> (56 - 8 * i));
    }
    // VideoToolbox's H.264 is AVCC already, with 4-byte lengths.
    if (CMBlockBufferCopyDataBytes(block, 0, total, p + 9) != kCMBlockBufferNoErr) {
        return;
    }
    emit(KindFrame, out);
}

static void stopEncoder(void) {
    if (session) {
        VTCompressionSessionCompleteFrames(session, kCMTimeInvalid);
        VTCompressionSessionInvalidate(session);
        CFRelease(session);
        session = NULL;
    }
    if (transfer) {
        VTPixelTransferSessionInvalidate(transfer);
        CFRelease(transfer);
        transfer = NULL;
    }
    if (pool) {
        CVPixelBufferPoolRelease(pool);
        pool = NULL;
    }
    sourceWidth = sourceHeight = 0;
    codec = nil;
    avcCSent = nil;
}

static void setProperty(CFStringRef key, CFTypeRef value) {
    OSStatus st = VTSessionSetProperty(session, key, value);
    if (st != noErr) {
        fprintf(stderr, "encoder: setting %s: %d\n", [(__bridge NSString *)key UTF8String], (int)st);
    }
}

// startEncoder makes an encoder for a screen of w×h, scaled so its longer
// side is at most maxSide.
static BOOL startEncoder(size_t w, size_t h) {
    stopEncoder();
    double scale = MIN(1.0, (double)maxSide / (double)MAX(w, h));
    encodeWidth = (int)((double)w * scale) & ~1;
    encodeHeight = (int)((double)h * scale) & ~1;
    NSDictionary *attrs = @{
        (__bridge NSString *)kCVPixelBufferPixelFormatTypeKey : @(kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange),
        (__bridge NSString *)kCVPixelBufferWidthKey : @(encodeWidth),
        (__bridge NSString *)kCVPixelBufferHeightKey : @(encodeHeight),
        (__bridge NSString *)kCVPixelBufferIOSurfacePropertiesKey : @{},
    };
    if (CVPixelBufferPoolCreate(NULL, NULL, (__bridge CFDictionaryRef)attrs, &pool) != kCVReturnSuccess) {
        report(@"the encoder's buffers couldn't be made");
        return NO;
    }
    if (VTPixelTransferSessionCreate(NULL, &transfer) != noErr) {
        report(@"the screen couldn't be scaled for the encoder");
        return NO;
    }
    NSDictionary *requireHardware = @{(__bridge NSString *)kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder : @YES};
    OSStatus st = VTCompressionSessionCreate(NULL, encodeWidth, encodeHeight, kCMVideoCodecType_H264, (__bridge CFDictionaryRef)requireHardware,
                                             NULL, NULL, encoded, NULL, &session);
    hardware = st == noErr;
    if (!hardware) {
        fprintf(stderr, "encoder: no hardware H.264 encoder (%d), encoding in software\n", (int)st);
        st = VTCompressionSessionCreate(NULL, encodeWidth, encodeHeight, kCMVideoCodecType_H264, NULL, NULL, NULL, encoded, NULL, &session);
    }
    if (st != noErr) {
        report([NSString stringWithFormat:@"no H.264 encoder: %d", (int)st]);
        return NO;
    }
    setProperty(kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
    setProperty(kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
    setProperty(kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_Main_AutoLevel);
    setProperty(kVTCompressionPropertyKey_MaxKeyFrameInterval, (__bridge CFTypeRef) @600);
    setProperty(kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, (__bridge CFTypeRef) @10);
    setProperty(kVTCompressionPropertyKey_ExpectedFrameRate, (__bridge CFTypeRef) @60);
    setProperty(kVTCompressionPropertyKey_AverageBitRate, (__bridge CFTypeRef) @(encodeWidth * encodeHeight * 4));
    VTCompressionSessionPrepareToEncodeFrames(session);
    sourceWidth = w;
    sourceHeight = h;
    forceKey = YES;
    return YES;
}

static void encodeSurface(uint64_t now) {
    size_t w = IOSurfaceGetWidth(surface), h = IOSurfaceGetHeight(surface);
    if ((w != sourceWidth || h != sourceHeight || !session) && !startEncoder(w, h)) {
        return;
    }
    CVPixelBufferRef src = NULL, dst = NULL;
    if (CVPixelBufferCreateWithIOSurface(NULL, surface, NULL, &src) != kCVReturnSuccess) {
        return;
    }
    // A copy, scaled and in the encoder's format, so the simulator drawing
    // the next frame doesn't tear this one.
    CVReturn cr = CVPixelBufferPoolCreatePixelBuffer(NULL, pool, &dst);
    OSStatus st = cr == kCVReturnSuccess ? VTPixelTransferSessionTransferImage(transfer, src, dst) : (OSStatus)cr;
    CVPixelBufferRelease(src);
    if (st != noErr) {
        if (dst) {
            CVPixelBufferRelease(dst);
        }
        return;
    }
    NSDictionary *options = forceKey ? @{(__bridge NSString *)kVTEncodeFrameOptionKey_ForceKeyFrame : @YES} : nil;
    forceKey = NO;
    CMTime pts = CMTimeMake((int64_t)micros(now - started), 1000000);
    VTCompressionSessionEncodeFrame(session, dst, pts, kCMTimeInvalid, (__bridge CFDictionaryRef)options, NULL, NULL);
    CVPixelBufferRelease(dst);
    lastEncoded = now;
}

static uint32_t seed;
static unsigned ticks;

// tick runs 60 times a second while the screen is streamed: it encodes a
// frame when the screen changed (its surface's seed moved, or the simulator
// said where it drew), when a keyframe is due, and at least 10 times a second
// in case neither says.
static void tick(void) {
    if (ticks++ % 30 == 0 || !surface) {
        refreshSurface();
    }
    if (!surface) {
        return;
    }
    uint64_t now = mach_absolute_time();
    uint32_t s = IOSurfaceGetSeed(surface);
    BOOL due = s != seed || atomic_exchange(&damaged, false) || forceKey || micros(now - lastEncoded) >= 100000;
    if (!due) {
        return;
    }
    seed = s;
    encodeSurface(now);
}

static void startStream(void) {
    if (timer) {
        forceKey = YES;
        return;
    }
    started = mach_absolute_time();
    startedWall = [[NSDate date] timeIntervalSince1970];
    lastEncoded = 0;
    forceKey = YES;
    timer = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, 0, captureQueue);
    dispatch_source_set_timer(timer, DISPATCH_TIME_NOW, NSEC_PER_SEC / 60, NSEC_PER_MSEC);
    dispatch_source_set_event_handler(timer, ^{
      @autoreleasepool {
          tick();
      }
    });
    dispatch_resume(timer);
}

static void stopStream(void) {
    if (timer) {
        dispatch_source_cancel(timer);
        timer = nil;
    }
    stopEncoder();
}

#pragma mark - Input

static BOOL inputReady, useDTUHID;
static xpc_connection_t hid;
static id legacy;
static IndigoButtonFn indigoButton;
static IndigoKeyboardFn indigoKeyboard;
static IndigoHIDArbitraryFn indigoHIDArbitrary;
static IndigoMouseFn indigoMouse;
static BOOL touching;

static const char *digitizerService = "com.apple.coredevice.feature.remote.hid.digitizer";

typedef xpc_object_t (*EndpointFromPortFn)(mach_port_t port, uint64_t a, uint64_t b);
typedef void (*EnableSim2HostFn)(xpc_connection_t connection);

static xpc_object_t dtuhidMessage(const char *type, xpc_object_t payload, bool barrier) {
    xpc_object_t m = xpc_dictionary_create(NULL, NULL, 0);
    xpc_dictionary_set_string(m, "messageType", type);
    xpc_dictionary_set_bool(m, "isBarrier", barrier);
    xpc_dictionary_set_string(m, "featureIdentifier", digitizerService);
    xpc_dictionary_set_value(m, "payload", payload);
    return m;
}

static xpc_object_t keyPayload(uint64_t usage, BOOL down) {
    xpc_object_t p = xpc_dictionary_create(NULL, NULL, 0);
    xpc_dictionary_set_uint64(p, "usageCode", usage);
    xpc_dictionary_set_uint64(p, "state", down ? 1 : 2);
    return p;
}

static BOOL connectDTUHID(void) {
    NSError *error = nil;
    mach_port_t port = MACH_PORT_NULL;
    @try {
        port = [(id<ABSimDevice>)device lookup:@(digitizerService) error:&error];
    } @catch (NSException *e) {
        report([NSString stringWithFormat:@"dtuhidd: %@", e.reason]);
        return NO;
    }
    if (port == MACH_PORT_NULL) {
        report([NSString stringWithFormat:@"dtuhidd isn't there: %@", error.localizedDescription ?: @"is the simulator booted?"]);
        return NO;
    }
    EndpointFromPortFn endpointFromPort = (EndpointFromPortFn)dlsym(RTLD_DEFAULT, "xpc_endpoint_create_mach_port_4sim");
    EnableSim2HostFn enableSim2Host = (EnableSim2HostFn)dlsym(RTLD_DEFAULT, "xpc_connection_enable_sim2host_4sim");
    if (!endpointFromPort || !enableSim2Host) {
        report(@"this macOS can't connect to a simulator's services");
        return NO;
    }
    xpc_object_t endpoint = endpointFromPort(port, 0, 0);
    if (!endpoint) {
        report(@"dtuhidd: no endpoint");
        return NO;
    }
    hid = xpc_connection_create_from_endpoint((xpc_endpoint_t)endpoint);
    enableSim2Host(hid);
    xpc_connection_set_target_queue(hid, hidQueue);
    xpc_connection_set_event_handler(hid, ^(xpc_object_t event) {
      if (event == XPC_ERROR_CONNECTION_INVALID) {
          hid = nil;
          inputReady = NO;
          report(@"dtuhidd closed the connection");
      }
    });
    xpc_connection_resume(hid);
    // A barrier that dtuhidd answers says it's listening.
    xpc_connection_send_message_with_reply(hid, dtuhidMessage("IndigoKeyboardButtonEvent", keyPayload(0, NO), true), hidQueue, ^(xpc_object_t reply) {
      if (xpc_get_type(reply) == XPC_TYPE_ERROR) {
          report(@"dtuhidd didn't answer");
      }
    });
    return YES;
}

static BOOL connectLegacy(void) {
    if (!simulatorKit) {
        report(@"SimulatorKit didn't load, so input can't reach the simulator");
        return NO;
    }
    indigoButton = (IndigoButtonFn)dlsym(simulatorKit, "IndigoHIDMessageForButton");
    indigoKeyboard = (IndigoKeyboardFn)dlsym(simulatorKit, "IndigoHIDMessageForKeyboardArbitrary");
    indigoHIDArbitrary = (IndigoHIDArbitraryFn)dlsym(simulatorKit, "IndigoHIDMessageForHIDArbitrary");
    indigoMouse = (IndigoMouseFn)dlsym(simulatorKit, "IndigoHIDMessageForMouseNSEvent");
    Class client = NSClassFromString(@"SimulatorKit.SimDeviceLegacyHIDClient");
    if (!client || !indigoButton || !indigoKeyboard || !indigoMouse) {
        report(@"SimulatorKit has no Indigo HID client");
        return NO;
    }
    NSError *error = nil;
    @try {
        legacy = [(id<ABLegacyHIDClient>)[client alloc] initWithDevice:device error:&error];
    } @catch (NSException *e) {
        report([NSString stringWithFormat:@"Indigo HID: %@", e.reason]);
        return NO;
    }
    if (!legacy) {
        report([NSString stringWithFormat:@"Indigo HID: %@", error.localizedDescription ?: @"no client"]);
        return NO;
    }
    return YES;
}

static BOOL ensureInput(void) {
    if (inputReady) {
        return YES;
    }
    if (deviceState(device) != SimDeviceStateBooted) {
        report(@"the simulator isn't booted");
        return NO;
    }
    NSString *version = [NSBundle bundleForClass:NSClassFromString(@"SimDevice")].infoDictionary[@"CFBundleVersion"];
    useDTUHID = version && [version compare:@"1155.4" options:NSNumericSearch] != NSOrderedAscending;
    inputReady = useDTUHID ? connectDTUHID() : connectLegacy();
    if (inputReady) {
        emitJSON(KindEvent, @{@"input" : useDTUHID ? @"dtuhid" : @"indigo", @"coreSimulator" : version ?: @""});
    }
    return inputReady;
}

static void sendLegacy(IndigoMessage *message) {
    if (!message) {
        return;
    }
    @try {
        [(id<ABLegacyHIDClient>)legacy sendWithMessage:message
                                          freeWhenDone:YES
                                       completionQueue:hidQueue
                                            completion:^(NSError *error) {
                                              if (error) {
                                                  report([NSString stringWithFormat:@"Indigo HID: %@", error.localizedDescription]);
                                              }
                                            }];
    } @catch (NSException *e) {
        report([NSString stringWithFormat:@"Indigo HID: %@", e.reason]);
    }
}

// legacyTouch is one touch as Simulator.app sends it: the mouse event
// SimulatorKit builds, its point as fractions of the screen, copied into a
// touch message whose second payload marks it as the contact.
static IndigoMessage *legacyTouch(double x, double y, BOOL down) {
    CGPoint point = CGPointMake(x, y);
    IndigoMessage *mouse = indigoMouse(&point, NULL, 0x32, down ? 1 : 2, CGSizeMake(1, 1), 0);
    if (!mouse) {
        return NULL;
    }
    mouse->payload.event.touch.xRatio = x;
    mouse->payload.event.touch.yRatio = y;
    size_t stride = sizeof(IndigoPayload);
    IndigoMessage *m = calloc(1, sizeof(IndigoMessage) + stride);
    m->innerSize = (unsigned int)stride;
    m->eventType = 2; // touch
    m->payload.eventKind = 0xB;
    m->payload.timestamp = mach_absolute_time();
    memcpy(&m->payload.event.touch, &mouse->payload.event.touch, sizeof(IndigoTouch));
    free(mouse);
    IndigoPayload *second = (IndigoPayload *)((uint8_t *)m + offsetof(IndigoMessage, payload) + stride);
    memcpy(second, &m->payload, stride);
    second->event.touch.field1 = 1;
    second->event.touch.field2 = 2;
    return m;
}

static void sendTouch(NSString *phase, double x, double y) {
    BOOL down = ![phase isEqualToString:@"up"];
    if (useDTUHID) {
        uint64_t type = 1; // position
        if (!down) {
            type = 2; // end
        } else if (!touching) {
            type = 0; // start
        }
        xpc_object_t point = xpc_dictionary_create(NULL, NULL, 0);
        xpc_dictionary_set_double(point, "x", x);
        xpc_dictionary_set_double(point, "y", y);
        xpc_object_t p = xpc_dictionary_create(NULL, NULL, 0);
        xpc_dictionary_set_value(p, "pointOne", point);
        xpc_dictionary_set_uint64(p, "eventType", type);
        xpc_dictionary_set_uint64(p, "edge", 0);
        xpc_dictionary_set_uint64(p, "target", 0);
        xpc_connection_send_message(hid, dtuhidMessage("IndigoDigitizerEvent", p, false));
    } else {
        // Indigo has no move: a finger that moves is down at its new point.
        sendLegacy(legacyTouch(x, y, down));
    }
    touching = down;
}

static void sendKey(uint64_t usage, BOOL down) {
    if (useDTUHID) {
        xpc_connection_send_message(hid, dtuhidMessage("IndigoKeyboardButtonEvent", keyPayload(usage, down), false));
    } else {
        sendLegacy(indigoKeyboard((int32_t)usage, down ? 1 : 2));
    }
}

// Buttons, by the HID consumer usage dtuhidd takes, and the Indigo button
// source before it (-1: none, so its consumer usage goes as HID arbitrary).
static void sendButton(NSString *name, BOOL down) {
    NSDictionary *usages = @{@"home" : @0x40, @"lock" : @0x30, @"siri" : @0xCF, @"volumeUp" : @0xE9, @"volumeDown" : @0xEA};
    NSDictionary *sources = @{@"home" : @0x0, @"lock" : @0x1, @"siri" : @0x400002};
    NSNumber *usage = usages[name];
    if (!usage) {
        report([NSString stringWithFormat:@"no button %@", name]);
        return;
    }
    if (useDTUHID) {
        xpc_object_t p = xpc_dictionary_create(NULL, NULL, 0);
        xpc_dictionary_set_uint64(p, "usagePage", 0x0C);
        xpc_dictionary_set_uint64(p, "usageCode", usage.unsignedLongLongValue);
        xpc_dictionary_set_uint64(p, "state", down ? 1 : 2);
        xpc_connection_send_message(hid, dtuhidMessage("IndigoButtonEvent", p, false));
    } else if (sources[name]) {
        sendLegacy(indigoButton([sources[name] intValue], down ? 1 : 2, 0x33));
    } else if (indigoHIDArbitrary) {
        sendLegacy(indigoHIDArbitrary(0x32, 0x0C, usage.unsignedIntValue, down ? 1 : 2));
    }
}

#pragma mark - Ops

static void handle(NSDictionary *o) {
    NSString *name = o[@"op"];
    if (![name isKindOfClass:[NSString class]]) {
        return;
    }
    if ([name isEqualToString:@"stream"]) {
        BOOL on = [o[@"on"] boolValue];
        dispatch_async(captureQueue, ^{
          if (on) {
              startStream();
          } else {
              stopStream();
          }
        });
        return;
    }
    if ([name isEqualToString:@"keyframe"]) {
        dispatch_async(captureQueue, ^{
          forceKey = YES;
        });
        return;
    }
    dispatch_async(hidQueue, ^{
      if (!ensureInput()) {
          return;
      }
      if ([name isEqualToString:@"touch"]) {
          sendTouch(o[@"phase"], [o[@"x"] doubleValue], [o[@"y"] doubleValue]);
      } else if ([name isEqualToString:@"key"]) {
          sendKey([o[@"usage"] unsignedLongLongValue], [o[@"down"] boolValue]);
      } else if ([name isEqualToString:@"button"]) {
          sendButton(o[@"button"], [o[@"down"] boolValue]);
      }
    });
}

static void readOps(void) {
    char *line = NULL;
    size_t cap = 0;
    ssize_t n;
    while ((n = getline(&line, &cap, stdin)) > 0) {
        @autoreleasepool {
            NSData *data = [NSData dataWithBytes:line length:(NSUInteger)n];
            id o = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
            if ([o isKindOfClass:[NSDictionary class]]) {
                handle(o);
            }
        }
    }
    exit(0); // the helper has gone
}

int main(int argc, char **argv) {
    @autoreleasepool {
        if (argc != 2) {
            fprintf(stderr, "usage: DEVELOPER_DIR=… simhost <udid>\n");
            return 2;
        }
        signal(SIGPIPE, SIG_IGN);
        mach_timebase_info(&timebase);
        outQueue = dispatch_queue_create("agentbox.simhost.out", DISPATCH_QUEUE_SERIAL);
        captureQueue = dispatch_queue_create("agentbox.simhost.capture",
                                             dispatch_queue_attr_make_with_qos_class(DISPATCH_QUEUE_SERIAL, QOS_CLASS_USER_INTERACTIVE, 0));
        hidQueue = dispatch_queue_create("agentbox.simhost.hid", DISPATCH_QUEUE_SERIAL);
        const char *dev = getenv("DEVELOPER_DIR");
        developerDir = dev ? @(dev) : @"/Applications/Xcode.app/Contents/Developer";
        if (!loadFrameworks() || !(device = findDevice(@(argv[1])))) {
            // Said why on stdout; stay until the helper has read it.
            dispatch_sync(outQueue, ^{
            });
            return 1;
        }
        // Say the screen's size as soon as there is one, for taps in pixels,
        // checking every second while the simulator boots.
        dispatch_source_t finder = dispatch_source_create(DISPATCH_SOURCE_TYPE_TIMER, 0, 0, captureQueue);
        dispatch_source_set_timer(finder, DISPATCH_TIME_NOW, NSEC_PER_SEC, NSEC_PER_SEC / 10);
        dispatch_source_set_event_handler(finder, ^{
          if (!timer) {
              refreshSurface();
          }
        });
        dispatch_resume(finder);
        [NSThread detachNewThreadWithBlock:^{
          readOps();
        }];
        dispatch_main();
    }
}
