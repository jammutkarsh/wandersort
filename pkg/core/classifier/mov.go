package classifier

type Mov struct {
	AEBBracketValue                                 int      `json:"AEBBracketValue"`
	AFAssistBeam                                    int      `json:"AFAssistBeam"`
	ActionVideoStabilizationStrength                int      `json:"Action-videoStabilization-strength"`
	AlternateFormatTrack                            int      `json:"AlternateFormatTrack"`
	Aperture                                        string   `json:"Aperture"`
	ApertureValue                                   float64  `json:"ApertureValue"`
	AppleMakerNote74                                int      `json:"Apple-maker-note74"`
	AppleMakerNote97                                int      `json:"Apple-maker-note97"`
	ApplePhotosVariationIdentifier                  int      `json:"ApplePhotosVariationIdentifier"`
	AppleProappsCameraName                          string   `json:"AppleProappsCameraName"`
	AppleProappsClipID                              string   `json:"AppleProappsClipID"`
	AppleProappsIsGood                              int      `json:"AppleProappsIsGood"`
	AppleProappsLogNote                             string   `json:"AppleProappsLogNote"`
	AppleProappsManufacturer                        string   `json:"AppleProappsManufacturer"`
	AppleProappsReel                                int      `json:"AppleProappsReel"`
	AppleProappsScene                               int      `json:"AppleProappsScene"`
	AppleProappsShot                                int      `json:"AppleProappsShot"`
	AudioBitsPerSample                              int      `json:"AudioBitsPerSample"`
	AudioChannels                                   int      `json:"AudioChannels"`
	AudioFormat                                     string   `json:"AudioFormat"`
	AudioSampleRate                                 int      `json:"AudioSampleRate"`
	Author                                          string   `json:"Author"`
	AutoExposureBracketing                          int      `json:"AutoExposureBracketing"`
	AutoISO                                         int      `json:"AutoISO"`
	AutoLightingOptimizer                           int      `json:"AutoLightingOptimizer"`
	AverageBitrate                                  int      `json:"AverageBitrate"`
	AvgBitrate                                      int      `json:"AvgBitrate"`
	BackgroundColor                                 string   `json:"BackgroundColor"`
	Balance                                         int      `json:"Balance"`
	BaseISO                                         int      `json:"BaseISO"`
	BitDepth                                        int      `json:"BitDepth"`
	BitsPerSample                                   int      `json:"BitsPerSample"`
	BlackMaskBottomBorder                           int      `json:"BlackMaskBottomBorder"`
	BlackMaskLeftBorder                             int      `json:"BlackMaskLeftBorder"`
	BlackMaskRightBorder                            int      `json:"BlackMaskRightBorder"`
	BlackMaskTopBorder                              int      `json:"BlackMaskTopBorder"`
	BlackmagicDesignCameraAperture                  string   `json:"Blackmagic-designCameraAperture"`
	BlackmagicDesignCameraCameraOp                  string   `json:"Blackmagic-designCameraCameraOp"`
	BlackmagicDesignCameraDateRecorded              string   `json:"Blackmagic-designCameraDateRecorded"`
	BlackmagicDesignCameraDayNight                  string   `json:"Blackmagic-designCameraDayNight"`
	BlackmagicDesignCameraDirector                  string   `json:"Blackmagic-designCameraDirector"`
	BlackmagicDesignCameraEnvironment               string   `json:"Blackmagic-designCameraEnvironment"`
	BlackmagicDesignCameraIso                       int      `json:"Blackmagic-designCameraIso"`
	BlackmagicDesignCameraLensType                  string   `json:"Blackmagic-designCameraLensType"`
	BlackmagicDesignCameraProjectName               string   `json:"Blackmagic-designCameraProjectName"`
	BlackmagicDesignCameraWhiteBalanceKelvin        int      `json:"Blackmagic-designCameraWhiteBalanceKelvin"`
	BlackmagicDesignCameraWhiteBalanceTint          int      `json:"Blackmagic-designCameraWhiteBalanceTint"`
	BlackmagicDesignSensorFPS                       float64  `json:"Blackmagic-designSensorFPS"`
	BlackmagicDesignShutterMode                     string   `json:"Blackmagic-designShutterMode"`
	BlackmagicDesignShutterSpeed                    string   `json:"Blackmagic-designShutterSpeed"`
	BracketMode                                     int      `json:"BracketMode"`
	BracketShotNumber                               int      `json:"BracketShotNumber"`
	BracketValue                                    int      `json:"BracketValue"`
	BufferSize                                      int      `json:"BufferSize"`
	BulbDuration                                    int      `json:"BulbDuration"`
	CameraISO                                       string   `json:"CameraISO"`
	CameraLensIrisfnumber                           string   `json:"CameraLensIrisfnumber"`
	CameraLensIrisfnumberEngUS                      string   `json:"CameraLensIrisfnumber-eng-US"`
	CameraTemperature                               int      `json:"CameraTemperature"`
	CameraType                                      int      `json:"CameraType"`
	CanonExposureMode                               int      `json:"CanonExposureMode"`
	CanonFirmwareVersion                            string   `json:"CanonFirmwareVersion"`
	CanonFlashMode                                  int      `json:"CanonFlashMode"`
	CanonImageSize                                  int      `json:"CanonImageSize"`
	CanonImageType                                  string   `json:"CanonImageType"`
	CanonModelID                                    int64    `json:"CanonModelID"`
	CaptureMode                                     string   `json:"CaptureMode"`
	CinematicVideo                                  string   `json:"Cinematic-video"`
	CinematicVideoIntent                            int      `json:"Cinematic-video-intent"`
	CircleOfConfusion                               float64  `json:"CircleOfConfusion"`
	CleanApertureDimensions                         string   `json:"CleanApertureDimensions"`
	ColorComponents                                 int      `json:"ColorComponents"`
	ColorPrimaries                                  int      `json:"ColorPrimaries"`
	ColorProfiles                                   string   `json:"ColorProfiles"`
	ColorSpace                                      int      `json:"ColorSpace"`
	ColorTemperature                                int      `json:"ColorTemperature"`
	ColorTone                                       int      `json:"ColorTone"`
	CompatibleBrands                                []string `json:"CompatibleBrands"`
	ComponentsConfiguration                         string   `json:"ComponentsConfiguration"`
	CompressionFormat                               int      `json:"CompressionFormat"`
	CompressorID                                    string   `json:"CompressorID"`
	CompressorName                                  string   `json:"CompressorName"`
	CompressorVersion                               string   `json:"CompressorVersion"`
	ContentDescribes                                int      `json:"ContentDescribes"`
	ContentIdentifier                               string   `json:"ContentIdentifier"`
	ContinuousDrive                                 int      `json:"ContinuousDrive"`
	Contrast                                        int      `json:"Contrast"`
	ControlMode                                     int      `json:"ControlMode"`
	CreateDate                                      string   `json:"CreateDate"`
	CreationDate                                    string   `json:"CreationDate"`
	CreationDateEngIN                               string   `json:"CreationDate-eng-IN"`
	CropBottomMargin                                int      `json:"CropBottomMargin"`
	CropLeftMargin                                  int      `json:"CropLeftMargin"`
	CropRightMargin                                 int      `json:"CropRightMargin"`
	CropTopMargin                                   int      `json:"CropTopMargin"`
	CurrentTime                                     int      `json:"CurrentTime"`
	CustomPictureStyleFileName                      string   `json:"CustomPictureStyleFileName"`
	CustomRendered                                  int      `json:"CustomRendered"`
	Dof                                             string   `json:"DOF"`
	DateTimeOriginal                                string   `json:"DateTimeOriginal"`
	DaylightSavings                                 int      `json:"DaylightSavings"`
	DigitalGain                                     int      `json:"DigitalGain"`
	DigitalZoom                                     int      `json:"DigitalZoom"`
	Director                                        string   `json:"Director"`
	Directory                                       string   `json:"Directory"`
	DriveMode                                       int      `json:"DriveMode"`
	Duration                                        float64  `json:"Duration"`
	EasyMode                                        int      `json:"EasyMode"`
	EncodedPixelsDimensions                         string   `json:"EncodedPixelsDimensions"`
	Encoder                                         string   `json:"Encoder"`
	EncodingProcess                                 int      `json:"EncodingProcess"`
	ExifByteOrder                                   string   `json:"ExifByteOrder"`
	ExifImageHeight                                 int      `json:"ExifImageHeight"`
	ExifImageWidth                                  int      `json:"ExifImageWidth"`
	ExifToolVersion                                 float64  `json:"ExifToolVersion"`
	ExifVersion                                     string   `json:"ExifVersion"`
	ExposureCompensation                            int      `json:"ExposureCompensation"`
	ExposureLevelIncrements                         int      `json:"ExposureLevelIncrements"`
	ExposureMode                                    int      `json:"ExposureMode"`
	ExposureProgram                                 int      `json:"ExposureProgram"`
	ExposureTime                                    string   `json:"ExposureTime"`
	ExtendedLanguageTag                             string   `json:"ExtendedLanguageTag"`
	FNumber                                         string   `json:"FNumber"`
	Fov                                             float64  `json:"FOV"`
	FileAccessDate                                  string   `json:"FileAccessDate"`
	FileInodeChangeDate                             string   `json:"FileInodeChangeDate"`
	FileModifyDate                                  string   `json:"FileModifyDate"`
	FileName                                        string   `json:"FileName"`
	FilePermissions                                 int      `json:"FilePermissions"`
	FileSize                                        int      `json:"FileSize"`
	FileType                                        string   `json:"FileType"`
	FileTypeExtension                               string   `json:"FileTypeExtension"`
	Flash                                           int      `json:"Flash"`
	FlashBits                                       int      `json:"FlashBits"`
	FlashExposureComp                               int      `json:"FlashExposureComp"`
	FlashExposureLock                               int      `json:"FlashExposureLock"`
	FlashGuideNumber                                int      `json:"FlashGuideNumber"`
	FlashModel                                      int      `json:"FlashModel"`
	FlashpixVersion                                 string   `json:"FlashpixVersion"`
	FocalLength                                     int      `json:"FocalLength"`
	FocalLength35Efl                                float64  `json:"FocalLength35efl"`
	FocalLengthIn35MmFormat                         int      `json:"FocalLengthIn35mmFormat"`
	FocalLengthIn35MmFormatEngIN                    int      `json:"FocalLengthIn35mmFormat-eng-IN"`
	FocalLengthIn35MmFormatEngUS                    int      `json:"FocalLengthIn35mmFormat-eng-US"`
	FocalPlaneResolutionUnit                        int      `json:"FocalPlaneResolutionUnit"`
	FocalPlaneXResolution                           float64  `json:"FocalPlaneXResolution"`
	FocalPlaneYResolution                           float64  `json:"FocalPlaneYResolution"`
	FocalUnits                                      int      `json:"FocalUnits"`
	FocusDistanceLower                              float64  `json:"FocusDistanceLower"`
	FocusDistanceUpper                              float64  `json:"FocusDistanceUpper"`
	FocusMode                                       int      `json:"FocusMode"`
	FocusRange                                      int      `json:"FocusRange"`
	FontName                                        string   `json:"FontName"`
	FullFrameRatePlaybackIntent                     int      `json:"FullFrameRatePlaybackIntent"`
	GPSAltitude                                     int      `json:"GPSAltitude"`
	GPSAltitudeRef                                  int      `json:"GPSAltitudeRef"`
	GPSCoordinates                                  string   `json:"GPSCoordinates"`
	GPSLatitude                                     float64  `json:"GPSLatitude"`
	GPSLongitude                                    float64  `json:"GPSLongitude"`
	GPSPosition                                     string   `json:"GPSPosition"`
	GenBalance                                      int      `json:"GenBalance"`
	GenFlags                                        string   `json:"GenFlags"`
	GenGraphicsMode                                 int      `json:"GenGraphicsMode"`
	GenMediaVersion                                 int      `json:"GenMediaVersion"`
	GenOpColor                                      string   `json:"GenOpColor"`
	GraphicsMode                                    int      `json:"GraphicsMode"`
	HandlerClass                                    string   `json:"HandlerClass"`
	HandlerDescription                              string   `json:"HandlerDescription"`
	HandlerType                                     string   `json:"HandlerType"`
	HandlerVendorID                                 string   `json:"HandlerVendorID"`
	HighISONoiseReduction                           int      `json:"HighISONoiseReduction"`
	HighlightTonePriority                           int      `json:"HighlightTonePriority"`
	HyperfocalDistance                              string   `json:"HyperfocalDistance"`
	Iso                                             int      `json:"ISO"`
	ISOExpansion                                    int      `json:"ISOExpansion"`
	ImageHeight                                     int      `json:"ImageHeight"`
	ImageSize                                       string   `json:"ImageSize"`
	ImageWidth                                      int      `json:"ImageWidth"`
	InternalSerialNumber                            string   `json:"InternalSerialNumber"`
	InteropIndex                                    string   `json:"InteropIndex"`
	InteropVersion                                  string   `json:"InteropVersion"`
	LCDDisplayAtPowerOn                             int      `json:"LCDDisplayAtPowerOn"`
	LayoutFlags                                     int      `json:"LayoutFlags"`
	Lens                                            int      `json:"Lens"`
	Lens35Efl                                       float64  `json:"Lens35efl"`
	LensID                                          string   `json:"LensID"`
	LensInfo                                        string   `json:"LensInfo"`
	LensModel                                       string   `json:"LensModel"`
	LensModelEngIN                                  string   `json:"LensModel-eng-IN"`
	LensModelEngUS                                  string   `json:"LensModel-eng-US"`
	LensSerialNumber                                string   `json:"LensSerialNumber"`
	LensType                                        int      `json:"LensType"`
	LightValue                                      float64  `json:"LightValue"`
	LivePhotoSubjectRelightingAppliedCurveParameter float64  `json:"Live-photoSubject-relighting-applied-curve-parameter"`
	LivePhotoAuto                                   int      `json:"LivePhotoAuto"`
	LivePhotoVitalityScore                          float64  `json:"LivePhotoVitalityScore"`
	LivePhotoVitalityScoringVersion                 int      `json:"LivePhotoVitalityScoringVersion"`
	LiveViewShooting                                int      `json:"LiveViewShooting"`
	LocationAccuracyHorizontal                      float64  `json:"LocationAccuracyHorizontal"`
	LongExposureNoiseReduction                      int      `json:"LongExposureNoiseReduction"`
	LoopStyle                                       int      `json:"LoopStyle"`
	MIMEType                                        string   `json:"MIMEType"`
	MacroMode                                       int      `json:"MacroMode"`
	MajorBrand                                      string   `json:"MajorBrand"`
	Make                                            string   `json:"Make"`
	MakeEngIN                                       string   `json:"Make-eng-IN"`
	ManualFlashOutput                               int      `json:"ManualFlashOutput"`
	MatrixCoefficients                              int      `json:"MatrixCoefficients"`
	MatrixStructure                                 string   `json:"MatrixStructure"`
	MaxAperture                                     float64  `json:"MaxAperture"`
	MaxBitrate                                      int      `json:"MaxBitrate"`
	MaxFocalLength                                  int      `json:"MaxFocalLength"`
	MeasuredEV                                      float64  `json:"MeasuredEV"`
	MeasuredEV2                                     float64  `json:"MeasuredEV2"`
	MediaCreateDate                                 string   `json:"MediaCreateDate"`
	MediaDataOffset                                 int      `json:"MediaDataOffset"`
	MediaDataSize                                   int      `json:"MediaDataSize"`
	MediaDuration                                   float64  `json:"MediaDuration"`
	MediaHeaderVersion                              int      `json:"MediaHeaderVersion"`
	MediaLanguageCode                               string   `json:"MediaLanguageCode"`
	MediaModifyDate                                 string   `json:"MediaModifyDate"`
	MediaTimeScale                                  int      `json:"MediaTimeScale"`
	Megapixels                                      float64  `json:"Megapixels"`
	MetaFormat                                      string   `json:"MetaFormat"`
	Metadata1                                       int      `json:"Metadata1"`
	MeteringMode                                    int      `json:"MeteringMode"`
	MinAperture                                     float64  `json:"MinAperture"`
	MinFocalLength                                  int      `json:"MinFocalLength"`
	MinorVersion                                    string   `json:"MinorVersion"`
	MirrorLockup                                    int      `json:"MirrorLockup"`
	Model                                           string   `json:"Model"`
	ModelEngIN                                      string   `json:"Model-eng-IN"`
	ModifyDate                                      string   `json:"ModifyDate"`
	MovieHeaderVersion                              int      `json:"MovieHeaderVersion"`
	NDFilter                                        int      `json:"NDFilter"`
	NextTrackID                                     int      `json:"NextTrackID"`
	OpColor                                         string   `json:"OpColor"`
	OpticalZoomCode                                 int      `json:"OpticalZoomCode"`
	Orientation                                     int      `json:"Orientation"`
	OtherFormat                                     string   `json:"OtherFormat"`
	OwnerName                                       string   `json:"OwnerName"`
	PeripheralIlluminationCorr                      int      `json:"PeripheralIlluminationCorr"`
	PictureStyle                                    int      `json:"PictureStyle"`
	PictureStylePC                                  string   `json:"PictureStylePC"`
	PictureStyleUserDef                             string   `json:"PictureStyleUserDef"`
	Pixeldensity                                    string   `json:"Pixeldensity"`
	PlaybackFrameRate                               float64  `json:"PlaybackFrameRate"`
	PosterTime                                      int      `json:"PosterTime"`
	PreferredRate                                   int      `json:"PreferredRate"`
	PreferredVolume                                 int      `json:"PreferredVolume"`
	PreviewDuration                                 int      `json:"PreviewDuration"`
	PreviewTime                                     int      `json:"PreviewTime"`
	ProductionApertureDimensions                    string   `json:"ProductionApertureDimensions"`
	PurchaseFileFormat                              string   `json:"PurchaseFileFormat"`
	Quality                                         int      `json:"Quality"`
	RawJpgSize                                      int      `json:"RawJpgSize"`
	RecommendedExposureIndex                        int      `json:"RecommendedExposureIndex"`
	RecordMode                                      int      `json:"RecordMode"`
	RelatedImageHeight                              int      `json:"RelatedImageHeight"`
	RelatedImageWidth                               int      `json:"RelatedImageWidth"`
	ResolutionUnit                                  int      `json:"ResolutionUnit"`
	Rotation                                        int      `json:"Rotation"`
	Saturation                                      int      `json:"Saturation"`
	ScaleFactor35Efl                                float64  `json:"ScaleFactor35efl"`
	SceneCaptureType                                int      `json:"SceneCaptureType"`
	SelectionDuration                               int      `json:"SelectionDuration"`
	SelectionTime                                   int      `json:"SelectionTime"`
	SelfTimer                                       int      `json:"SelfTimer"`
	SensitivityType                                 int      `json:"SensitivityType"`
	SensorBlueLevel                                 int      `json:"SensorBlueLevel"`
	SensorBottomBorder                              int      `json:"SensorBottomBorder"`
	SensorHeight                                    int      `json:"SensorHeight"`
	SensorLeftBorder                                int      `json:"SensorLeftBorder"`
	SensorRedLevel                                  int      `json:"SensorRedLevel"`
	SensorRightBorder                               int      `json:"SensorRightBorder"`
	SensorTopBorder                                 int      `json:"SensorTopBorder"`
	SensorWidth                                     int      `json:"SensorWidth"`
	SequenceNumber                                  int      `json:"SequenceNumber"`
	SerialNumber                                    string   `json:"SerialNumber"`
	SetButtonWhenShooting                           int      `json:"SetButtonWhenShooting"`
	Sharpness                                       int      `json:"Sharpness"`
	SharpnessFrequency                              int      `json:"SharpnessFrequency"`
	ShootingMode                                    int      `json:"ShootingMode"`
	ShutterButtonAFOnButton                         int      `json:"ShutterButtonAFOnButton"`
	ShutterMode                                     int      `json:"ShutterMode"`
	ShutterSpeed                                    string   `json:"ShutterSpeed"`
	ShutterSpeedValue                               int      `json:"ShutterSpeedValue"`
	SlowShutter                                     int      `json:"SlowShutter"`
	SmartstyleBypassed                              int      `json:"SmartstyleBypassed"`
	SmartstyleCast                                  int      `json:"SmartstyleCast"`
	SmartstyleColor                                 int      `json:"SmartstyleColor"`
	SmartstyleIntensity                             int      `json:"SmartstyleIntensity"`
	SmartstyleRenderingVersion                      int      `json:"SmartstyleRendering-version"`
	SmartstyleTone                                  int      `json:"SmartstyleTone"`
	Software                                        string   `json:"Software"`
	SoftwareEngIN                                   string   `json:"Software-eng-IN"`
	SourceFile                                      string   `json:"SourceFile"`
	SourceImageHeight                               int      `json:"SourceImageHeight"`
	SourceImageWidth                                int      `json:"SourceImageWidth"`
	SubSecCreateDate                                string   `json:"SubSecCreateDate"`
	SubSecDateTimeOriginal                          string   `json:"SubSecDateTimeOriginal"`
	SubSecModifyDate                                string   `json:"SubSecModifyDate"`
	SubSecTime                                      int      `json:"SubSecTime"`
	SubSecTimeDigitized                             int      `json:"SubSecTimeDigitized"`
	SubSecTimeOriginal                              int      `json:"SubSecTimeOriginal"`
	TargetAperture                                  int      `json:"TargetAperture"`
	TargetExposureTime                              int      `json:"TargetExposureTime"`
	TextColor                                       string   `json:"TextColor"`
	TextFace                                        int      `json:"TextFace"`
	TextFont                                        int      `json:"TextFont"`
	TextSize                                        int      `json:"TextSize"`
	ThumbnailImage                                  string   `json:"ThumbnailImage"`
	ThumbnailImageValidArea                         string   `json:"ThumbnailImageValidArea"`
	TimeScale                                       int      `json:"TimeScale"`
	TimeZone                                        int      `json:"TimeZone"`
	TimeZoneCity                                    int      `json:"TimeZoneCity"`
	TimecodeTrack                                   int      `json:"TimecodeTrack"`
	Title                                           string   `json:"Title"`
	ToneCurve                                       int      `json:"ToneCurve"`
	TrackCreateDate                                 string   `json:"TrackCreateDate"`
	TrackDuration                                   float64  `json:"TrackDuration"`
	TrackHeaderVersion                              int      `json:"TrackHeaderVersion"`
	TrackID                                         int      `json:"TrackID"`
	TrackLayer                                      int      `json:"TrackLayer"`
	TrackModifyDate                                 string   `json:"TrackModifyDate"`
	TrackVolume                                     int      `json:"TrackVolume"`
	TransferCharacteristics                         int      `json:"TransferCharacteristics"`
	UserComment                                     string   `json:"UserComment"`
	UserRating                                      int      `json:"UserRating"`
	VRDOffset                                       int      `json:"VRDOffset"`
	VendorID                                        string   `json:"VendorID"`
	VideoFrameRate                                  float64  `json:"VideoFrameRate"`
	VideoFullRangeFlag                              int      `json:"VideoFullRangeFlag"`
	WBBracketMode                                   int      `json:"WBBracketMode"`
	WBBracketValueAB                                int      `json:"WBBracketValueAB"`
	WBBracketValueGM                                int      `json:"WBBracketValueGM"`
	WBShiftAB                                       int      `json:"WBShiftAB"`
	WBShiftGM                                       int      `json:"WBShiftGM"`
	Warning                                         string   `json:"Warning"`
	WhiteBalance                                    int      `json:"WhiteBalance"`
	WhiteBalanceBlue                                int      `json:"WhiteBalanceBlue"`
	WhiteBalanceRed                                 int      `json:"WhiteBalanceRed"`
	XAttrComBlackmagicdesignFileinfo                string   `json:"XAttrComBlackmagicdesignFileinfo"`
	XAttrComBlackmagicdesignLocation                string   `json:"XAttrComBlackmagicdesignLocation"`
	XAttrComBlackmagicdesignThumbnail               string   `json:"XAttrComBlackmagicdesignThumbnail"`
	XAttrLastUsedDate                               string   `json:"XAttrLastUsedDate"`
	XAttrQuarantine                                 string   `json:"XAttrQuarantine"`
	XResolution                                     int      `json:"XResolution"`
	YCbCrPositioning                                int      `json:"YCbCrPositioning"`
	YCbCrSubSampling                                string   `json:"YCbCrSubSampling"`
	YResolution                                     int      `json:"YResolution"`
	ZoomSourceWidth                                 int      `json:"ZoomSourceWidth"`
	ZoomTargetWidth                                 int      `json:"ZoomTargetWidth"`
}

func (Mov) MediaType() string { return MediaTypeVideo }

func (m Mov) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:      ftoa(m.ExifToolVersion),
		SourceFile:           m.SourceFile,
		Directory:            m.Directory,
		FileName:             m.FileName,
		FileSize:             itoa(m.FileSize),
		FilePermissions:      itoa(m.FilePermissions),
		FileType:             m.FileType,
		FileTypeExtension:    m.FileTypeExtension,
		MIMEType:             m.MIMEType,
		FileModifyDate:       m.FileModifyDate,
		FileAccessDate:       m.FileAccessDate,
		FileInodeChangeDate:  m.FileInodeChangeDate,
		ImageWidth:           itoa(m.ImageWidth),
		ImageHeight:          itoa(m.ImageHeight),
		ImageSize:            m.ImageSize,
		Megapixels:           ftoa(m.Megapixels),
		Orientation:          itoa(m.Orientation),
		Make:                 m.Make,
		Model:                m.Model,
		LensModel:            m.LensModel,
		Software:             m.Software,
		CreateDate:           m.CreateDate,
		ModifyDate:           m.ModifyDate,
		DateTimeOriginal:     m.DateTimeOriginal,
		ISO:                  itoa(m.Iso),
		Aperture:             m.Aperture,
		FNumber:              m.FNumber,
		FocalLength:          itoa(m.FocalLength),
		ExposureTime:         m.ExposureTime,
		ShutterSpeed:         m.ShutterSpeed,
		ExposureMode:         itoa(m.ExposureMode),
		ExposureProgram:      itoa(m.ExposureProgram),
		ExposureCompensation: itoa(m.ExposureCompensation),
		Flash:                itoa(m.Flash),
		MeteringMode:         itoa(m.MeteringMode),
		WhiteBalance:         itoa(m.WhiteBalance),
		GPSLatitude:          ftoa(m.GPSLatitude),
		GPSLongitude:         ftoa(m.GPSLongitude),
		GPSAltitude:          itoa(m.GPSAltitude),
		GPSAltitudeRef:       itoa(m.GPSAltitudeRef),
		GPSPosition:          m.GPSPosition,
	}
}
