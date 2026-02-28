package classifier

type Dng struct {
	AEAverage                                          int      `json:"AEAverage"`
	AEStable                                           int      `json:"AEStable"`
	AETarget                                           int      `json:"AETarget"`
	AFConfidence                                       int      `json:"AFConfidence"`
	AFMeasuredDepth                                    int      `json:"AFMeasuredDepth"`
	AFPerformance                                      string   `json:"AFPerformance"`
	AFStable                                           int      `json:"AFStable"`
	AccelerationVector                                 string   `json:"AccelerationVector"`
	ActiveArea                                         string   `json:"ActiveArea"`
	AlreadyApplied                                     bool     `json:"AlreadyApplied"`
	AnalogBalance                                      string   `json:"AnalogBalance"`
	AntiAliasStrength                                  int      `json:"AntiAliasStrength"`
	Aperture                                           float64  `json:"Aperture"`
	ApertureValue                                      float64  `json:"ApertureValue"`
	ApproximateFocusDistance                           float64  `json:"ApproximateFocusDistance"`
	Artist                                             string   `json:"Artist"`
	AsShotNeutral                                      string   `json:"AsShotNeutral"`
	AsShotProfileName                                  string   `json:"AsShotProfileName"`
	AutoLateralCA                                      int      `json:"AutoLateralCA"`
	AutoTone                                           bool     `json:"AutoTone"`
	AutoToneDigest                                     string   `json:"AutoToneDigest"`
	AutoToneDigestNoSat                                string   `json:"AutoToneDigestNoSat"`
	AuxiliaryImageSubType                              string   `json:"AuxiliaryImageSubType"`
	AuxiliaryImageType                                 string   `json:"AuxiliaryImageType"`
	BaselineExposure                                   float64  `json:"BaselineExposure"`
	BaselineExposureOffset                             float64  `json:"BaselineExposureOffset"`
	BaselineNoise                                      int      `json:"BaselineNoise"`
	BaselineSharpness                                  int      `json:"BaselineSharpness"`
	BayerGreenSplit                                    int      `json:"BayerGreenSplit"`
	BestQualityScale                                   int      `json:"BestQualityScale"`
	BitsPerSample                                      int      `json:"BitsPerSample"`
	BlackLevel                                         string   `json:"BlackLevel"`
	BlackLevelRepeatDim                                string   `json:"BlackLevelRepeatDim"`
	Blacks2012                                         int      `json:"Blacks2012"`
	BlueHue                                            int      `json:"BlueHue"`
	BlueSaturation                                     int      `json:"BlueSaturation"`
	BrightnessValue                                    float64  `json:"BrightnessValue"`
	CFALayout                                          int      `json:"CFALayout"`
	CFAPattern                                         string   `json:"CFAPattern"`
	CFAPattern2                                        string   `json:"CFAPattern2"`
	CFAPlaneColor                                      string   `json:"CFAPlaneColor"`
	CFARepeatPatternDim                                string   `json:"CFARepeatPatternDim"`
	CacheVersion                                       string   `json:"CacheVersion"`
	CalibrationIlluminant1                             int      `json:"CalibrationIlluminant1"`
	CalibrationIlluminant2                             int      `json:"CalibrationIlluminant2"`
	CameraCalibration1                                 string   `json:"CameraCalibration1"`
	CameraCalibration2                                 string   `json:"CameraCalibration2"`
	CameraCalibrationSig                               string   `json:"CameraCalibrationSig"`
	CameraProfile                                      string   `json:"CameraProfile"`
	CameraProfileDigest                                string   `json:"CameraProfileDigest"`
	CameraSerialNumber                                 string   `json:"CameraSerialNumber"`
	CameraType                                         int      `json:"CameraType"`
	CircleOfConfusion                                  string   `json:"CircleOfConfusion"`
	Clarity2012                                        int      `json:"Clarity2012"`
	ColorGradeBlending                                 int      `json:"ColorGradeBlending"`
	ColorGradeGlobalHue                                int      `json:"ColorGradeGlobalHue"`
	ColorGradeGlobalLum                                int      `json:"ColorGradeGlobalLum"`
	ColorGradeGlobalSat                                int      `json:"ColorGradeGlobalSat"`
	ColorGradeHighlightLum                             int      `json:"ColorGradeHighlightLum"`
	ColorGradeMidtoneHue                               int      `json:"ColorGradeMidtoneHue"`
	ColorGradeMidtoneLum                               int      `json:"ColorGradeMidtoneLum"`
	ColorGradeMidtoneSat                               int      `json:"ColorGradeMidtoneSat"`
	ColorGradeShadowLum                                int      `json:"ColorGradeShadowLum"`
	ColorMatrix1                                       string   `json:"ColorMatrix1"`
	ColorMatrix2                                       string   `json:"ColorMatrix2"`
	ColorNoiseReduction                                int      `json:"ColorNoiseReduction"`
	ColorNoiseReductionDetail                          int      `json:"ColorNoiseReductionDetail"`
	ColorNoiseReductionSmoothness                      int      `json:"ColorNoiseReductionSmoothness"`
	ColorSpace                                         int      `json:"ColorSpace"`
	ColorTemperature                                   int      `json:"ColorTemperature"`
	Compression                                        int      `json:"Compression"`
	Contrast2012                                       int      `json:"Contrast2012"`
	CreateDate                                         string   `json:"CreateDate"`
	Creator                                            string   `json:"Creator"`
	CreatorTool                                        float64  `json:"CreatorTool"`
	CustomRendered                                     int      `json:"CustomRendered"`
	DNGBackwardVersion                                 string   `json:"DNGBackwardVersion"`
	DNGLensInfo                                        string   `json:"DNGLensInfo"`
	DNGPrivateData                                     string   `json:"DNGPrivateData"`
	DNGVersion                                         string   `json:"DNGVersion"`
	Dof                                                string   `json:"DOF"`
	DateCreated                                        string   `json:"DateCreated"`
	DateTimeOriginal                                   string   `json:"DateTimeOriginal"`
	DefaultBlackRender                                 int      `json:"DefaultBlackRender"`
	DefaultCropOrigin                                  string   `json:"DefaultCropOrigin"`
	DefaultCropSize                                    string   `json:"DefaultCropSize"`
	DefaultScale                                       string   `json:"DefaultScale"`
	DefringeGreenAmount                                int      `json:"DefringeGreenAmount"`
	DefringeGreenHueHi                                 int      `json:"DefringeGreenHueHi"`
	DefringeGreenHueLo                                 int      `json:"DefringeGreenHueLo"`
	DefringePurpleAmount                               int      `json:"DefringePurpleAmount"`
	DefringePurpleHueHi                                int      `json:"DefringePurpleHueHi"`
	DefringePurpleHueLo                                int      `json:"DefringePurpleHueLo"`
	Dehaze                                             int      `json:"Dehaze"`
	DerivedFrom                                        string   `json:"DerivedFrom"`
	DerivedFromDocumentID                              string   `json:"DerivedFromDocumentID"`
	DerivedFromInstanceID                              string   `json:"DerivedFromInstanceID"`
	DerivedFromOriginalDocumentID                      string   `json:"DerivedFromOriginalDocumentID"`
	DigitalZoomRatio                                   float64  `json:"DigitalZoomRatio"`
	Directory                                          string   `json:"Directory"`
	DistortionCorrectionAlreadyApplied                 bool     `json:"DistortionCorrectionAlreadyApplied"`
	DocumentID                                         string   `json:"DocumentID"`
	DynamicRange                                       int      `json:"DynamicRange"`
	EnhanceDenoiseAlreadyApplied                       bool     `json:"EnhanceDenoiseAlreadyApplied"`
	EnhanceDenoiseLumaAmount                           int      `json:"EnhanceDenoiseLumaAmount"`
	EnhanceDenoiseVersion                              int      `json:"EnhanceDenoiseVersion"`
	EnhanceParams                                      string   `json:"EnhanceParams"`
	ExifByteOrder                                      string   `json:"ExifByteOrder"`
	ExifImageHeight                                    int      `json:"ExifImageHeight"`
	ExifImageWidth                                     int      `json:"ExifImageWidth"`
	ExifToolVersion                                    float64  `json:"ExifToolVersion"`
	ExifVersion                                        string   `json:"ExifVersion"`
	Exposure2012                                       int      `json:"Exposure2012"`
	ExposureCompensation                               int      `json:"ExposureCompensation"`
	ExposureMode                                       int      `json:"ExposureMode"`
	ExposureProgram                                    int      `json:"ExposureProgram"`
	ExposureTime                                       float64  `json:"ExposureTime"`
	FNumber                                            float64  `json:"FNumber"`
	Fov                                                float64  `json:"FOV"`
	FileAccessDate                                     string   `json:"FileAccessDate"`
	FileInodeChangeDate                                string   `json:"FileInodeChangeDate"`
	FileModifyDate                                     string   `json:"FileModifyDate"`
	FileName                                           string   `json:"FileName"`
	FilePermissions                                    int      `json:"FilePermissions"`
	FileSize                                           int      `json:"FileSize"`
	FileType                                           string   `json:"FileType"`
	FileTypeExtension                                  string   `json:"FileTypeExtension"`
	Firmware                                           string   `json:"Firmware"`
	Flash                                              int      `json:"Flash"`
	FlashCompensation                                  int      `json:"FlashCompensation"`
	FocalLength                                        float64  `json:"FocalLength"`
	FocalLength35Efl                                   int      `json:"FocalLength35efl"`
	FocalLengthIn35MmFormat                            int      `json:"FocalLengthIn35mmFormat"`
	FocalPlaneResolutionUnit                           int      `json:"FocalPlaneResolutionUnit"`
	FocalPlaneXResolution                              float64  `json:"FocalPlaneXResolution"`
	FocalPlaneYResolution                              float64  `json:"FocalPlaneYResolution"`
	FocusDistanceRange                                 string   `json:"FocusDistanceRange"`
	FocusPosition                                      int      `json:"FocusPosition"`
	Format                                             string   `json:"Format"`
	ForwardMatrix1                                     string   `json:"ForwardMatrix1"`
	ForwardMatrix2                                     string   `json:"ForwardMatrix2"`
	GPSAltitude                                        float64  `json:"GPSAltitude"`
	GPSAltitudeRef                                     int      `json:"GPSAltitudeRef"`
	GPSDateStamp                                       string   `json:"GPSDateStamp"`
	GPSDateTime                                        string   `json:"GPSDateTime"`
	GPSDestBearing                                     float64  `json:"GPSDestBearing"`
	GPSDestBearingRef                                  string   `json:"GPSDestBearingRef"`
	GPSHPositioningError                               float64  `json:"GPSHPositioningError"`
	GPSImgDirection                                    float64  `json:"GPSImgDirection"`
	GPSImgDirectionRef                                 string   `json:"GPSImgDirectionRef"`
	GPSLatitude                                        float64  `json:"GPSLatitude"`
	GPSLatitudeRef                                     string   `json:"GPSLatitudeRef"`
	GPSLongitude                                       float64  `json:"GPSLongitude"`
	GPSLongitudeRef                                    string   `json:"GPSLongitudeRef"`
	GPSPosition                                        string   `json:"GPSPosition"`
	GPSSpeed                                           float64  `json:"GPSSpeed"`
	GPSSpeedRef                                        string   `json:"GPSSpeedRef"`
	GPSTimeStamp                                       string   `json:"GPSTimeStamp"`
	GPSVersionID                                       string   `json:"GPSVersionID"`
	GrainAmount                                        int      `json:"GrainAmount"`
	GrainSize                                          int      `json:"GrainSize"`
	GreenHue                                           int      `json:"GreenHue"`
	GreenSaturation                                    int      `json:"GreenSaturation"`
	HDREditMode                                        int      `json:"HDREditMode"`
	HDRGain                                            float64  `json:"HDRGain"`
	HDRHeadroom                                        float64  `json:"HDRHeadroom"`
	HDRMaxValue                                        string   `json:"HDRMaxValue"`
	HasCrop                                            bool     `json:"HasCrop"`
	HasSettings                                        bool     `json:"HasSettings"`
	Highlights2012                                     int      `json:"Highlights2012"`
	HintMaxOutputValue                                 float64  `json:"HintMaxOutputValue"`
	HistoryAction                                      []string `json:"HistoryAction"`
	HistoryChanged                                     string   `json:"HistoryChanged"`
	HistoryInstanceID                                  string   `json:"HistoryInstanceID"`
	HistoryParameters                                  string   `json:"HistoryParameters"`
	HistorySoftwareAgent                               string   `json:"HistorySoftwareAgent"`
	HistoryWhen                                        string   `json:"HistoryWhen"`
	HueAdjustmentAqua                                  int      `json:"HueAdjustmentAqua"`
	HueAdjustmentBlue                                  int      `json:"HueAdjustmentBlue"`
	HueAdjustmentGreen                                 int      `json:"HueAdjustmentGreen"`
	HueAdjustmentMagenta                               int      `json:"HueAdjustmentMagenta"`
	HueAdjustmentOrange                                int      `json:"HueAdjustmentOrange"`
	HueAdjustmentPurple                                int      `json:"HueAdjustmentPurple"`
	HueAdjustmentRed                                   int      `json:"HueAdjustmentRed"`
	HueAdjustmentYellow                                int      `json:"HueAdjustmentYellow"`
	HyperfocalDistance                                 float64  `json:"HyperfocalDistance"`
	Iso                                                int      `json:"ISO"`
	ImageCaptureType                                   int      `json:"ImageCaptureType"`
	ImageHeight                                        int      `json:"ImageHeight"`
	ImageNumber                                        int      `json:"ImageNumber"`
	ImageSize                                          string   `json:"ImageSize"`
	ImageWidth                                         int      `json:"ImageWidth"`
	InstanceID                                         string   `json:"InstanceID"`
	IsMergedPanorama                                   bool     `json:"IsMergedPanorama"`
	JXLDecodeSpeed                                     int      `json:"JXLDecodeSpeed"`
	JXLDistance                                        int      `json:"JXLDistance"`
	JXLEffort                                          int      `json:"JXLEffort"`
	LateralChromaticAberrationCorrectionAlreadyApplied bool     `json:"LateralChromaticAberrationCorrectionAlreadyApplied"`
	Lens                                               string   `json:"Lens"`
	LensID                                             string   `json:"LensID"`
	LensInfo                                           string   `json:"LensInfo"`
	LensMake                                           string   `json:"LensMake"`
	LensManualDistortionAmount                         int      `json:"LensManualDistortionAmount"`
	LensModel                                          string   `json:"LensModel"`
	LensProfileDistortionScale                         int      `json:"LensProfileDistortionScale"`
	LensProfileEnable                                  int      `json:"LensProfileEnable"`
	LensProfileName                                    string   `json:"LensProfileName"`
	LensProfileSetup                                   string   `json:"LensProfileSetup"`
	LensProfileVignettingScale                         int      `json:"LensProfileVignettingScale"`
	LensSerialNumber                                   string   `json:"LensSerialNumber"`
	LightValue                                         float64  `json:"LightValue"`
	LinearResponseLimit                                int      `json:"LinearResponseLimit"`
	LinearizationTable                                 string   `json:"LinearizationTable"`
	LivePhotoVideoIndex                                int      `json:"LivePhotoVideoIndex"`
	LocalizedCameraModel                               string   `json:"LocalizedCameraModel"`
	LookAmount                                         int      `json:"LookAmount"`
	LookCopyright                                      string   `json:"LookCopyright"`
	LookGroup                                          string   `json:"LookGroup"`
	LookName                                           string   `json:"LookName"`
	LookParametersBlacks2012                           int      `json:"LookParametersBlacks2012"`
	LookParametersCameraProfile                        string   `json:"LookParametersCameraProfile"`
	LookParametersConvertToGrayscale                   bool     `json:"LookParametersConvertToGrayscale"`
	LookParametersLookTable                            string   `json:"LookParametersLookTable"`
	LookParametersProcessVersion                       int      `json:"LookParametersProcessVersion"`
	LookParametersProfileGainTableMap                  int      `json:"LookParametersProfileGainTableMap"`
	LookParametersRGBTables                            int      `json:"LookParametersRGBTables"`
	LookParametersTexture                              string   `json:"LookParametersTexture"`
	LookParametersToneCurvePV2012                      []string `json:"LookParametersToneCurvePV2012"`
	LookParametersToneCurvePV2012Blue                  []string `json:"LookParametersToneCurvePV2012Blue"`
	LookParametersToneCurvePV2012Green                 []string `json:"LookParametersToneCurvePV2012Green"`
	LookParametersToneCurvePV2012Red                   []string `json:"LookParametersToneCurvePV2012Red"`
	LookParametersToneMapStrength                      int      `json:"LookParametersToneMapStrength"`
	LookParametersVersion                              int      `json:"LookParametersVersion"`
	LookSupportsAmount                                 bool     `json:"LookSupportsAmount"`
	LookSupportsMonochrome                             bool     `json:"LookSupportsMonochrome"`
	LookSupportsOutputReferred                         bool     `json:"LookSupportsOutputReferred"`
	LookUUID                                           string   `json:"LookUUID"`
	LuminanceAdjustmentAqua                            int      `json:"LuminanceAdjustmentAqua"`
	LuminanceAdjustmentBlue                            int      `json:"LuminanceAdjustmentBlue"`
	LuminanceAdjustmentGreen                           int      `json:"LuminanceAdjustmentGreen"`
	LuminanceAdjustmentMagenta                         int      `json:"LuminanceAdjustmentMagenta"`
	LuminanceAdjustmentOrange                          int      `json:"LuminanceAdjustmentOrange"`
	LuminanceAdjustmentPurple                          int      `json:"LuminanceAdjustmentPurple"`
	LuminanceAdjustmentRed                             int      `json:"LuminanceAdjustmentRed"`
	LuminanceAdjustmentYellow                          int      `json:"LuminanceAdjustmentYellow"`
	LuminanceNoiseReductionContrast                    int      `json:"LuminanceNoiseReductionContrast"`
	LuminanceNoiseReductionDetail                      int      `json:"LuminanceNoiseReductionDetail"`
	LuminanceSmoothing                                 int      `json:"LuminanceSmoothing"`
	MIMEType                                           string   `json:"MIMEType"`
	Make                                               string   `json:"Make"`
	MakerNoteVersion                                   int      `json:"MakerNoteVersion"`
	MaskSubArea                                        string   `json:"MaskSubArea"`
	MaxApertureValue                                   float64  `json:"MaxApertureValue"`
	Megapixels                                         float64  `json:"Megapixels"`
	MetadataDate                                       string   `json:"MetadataDate"`
	MeteringMode                                       int      `json:"MeteringMode"`
	Model                                              string   `json:"Model"`
	ModifyDate                                         string   `json:"ModifyDate"`
	NewRawImageDigest                                  string   `json:"NewRawImageDigest"`
	NoiseProfile                                       string   `json:"NoiseProfile"`
	NoiseReductionApplied                              int      `json:"NoiseReductionApplied"`
	OISMode                                            int      `json:"OISMode"`
	OffsetTime                                         string   `json:"OffsetTime"`
	OffsetTimeDigitized                                string   `json:"OffsetTimeDigitized"`
	OffsetTimeOriginal                                 string   `json:"OffsetTimeOriginal"`
	OpcodeList2                                        string   `json:"OpcodeList2"`
	OpcodeList3                                        string   `json:"OpcodeList3"`
	Orientation                                        int      `json:"Orientation"`
	OriginalDefaultCropSize                            string   `json:"OriginalDefaultCropSize"`
	OriginalDefaultFinalSize                           string   `json:"OriginalDefaultFinalSize"`
	OriginalDocumentID                                 string   `json:"OriginalDocumentID"`
	OtherImage                                         string   `json:"OtherImage"`
	OtherImageLength                                   int      `json:"OtherImageLength"`
	OtherImageStart                                    int      `json:"OtherImageStart"`
	OverrideLookVignette                               bool     `json:"OverrideLookVignette"`
	OwnerName                                          string   `json:"OwnerName"`
	PDRVersion                                         int      `json:"PDRVersion"`
	ParametricDarks                                    int      `json:"ParametricDarks"`
	ParametricHighlightSplit                           int      `json:"ParametricHighlightSplit"`
	ParametricHighlights                               int      `json:"ParametricHighlights"`
	ParametricLights                                   int      `json:"ParametricLights"`
	ParametricMidtoneSplit                             int      `json:"ParametricMidtoneSplit"`
	ParametricShadowSplit                              int      `json:"ParametricShadowSplit"`
	ParametricShadows                                  int      `json:"ParametricShadows"`
	PerspectiveAspect                                  int      `json:"PerspectiveAspect"`
	PerspectiveHorizontal                              int      `json:"PerspectiveHorizontal"`
	PerspectiveRotate                                  int      `json:"PerspectiveRotate"`
	PerspectiveScale                                   int      `json:"PerspectiveScale"`
	PerspectiveUpright                                 int      `json:"PerspectiveUpright"`
	PerspectiveVertical                                int      `json:"PerspectiveVertical"`
	PerspectiveX                                       int      `json:"PerspectiveX"`
	PerspectiveY                                       int      `json:"PerspectiveY"`
	PhotoIdentifier                                    string   `json:"PhotoIdentifier"`
	PhotometricInterpretation                          int      `json:"PhotometricInterpretation"`
	PhotosAppFeatureFlags                              int      `json:"PhotosAppFeatureFlags"`
	Pick                                               int      `json:"Pick"`
	PlanarConfiguration                                int      `json:"PlanarConfiguration"`
	PointColors                                        string   `json:"PointColors"`
	PortraitEffectsMatteVersion                        int      `json:"PortraitEffectsMatteVersion"`
	PostCropVignetteAmount                             int      `json:"PostCropVignetteAmount"`
	PreservedFileName                                  string   `json:"PreservedFileName"`
	PreviewApplicationName                             string   `json:"PreviewApplicationName"`
	PreviewApplicationVersion                          string   `json:"PreviewApplicationVersion"`
	PreviewColorSpace                                  int      `json:"PreviewColorSpace"`
	PreviewDateTime                                    string   `json:"PreviewDateTime"`
	PreviewImage                                       string   `json:"PreviewImage"`
	PreviewImageLength                                 int      `json:"PreviewImageLength"`
	PreviewImageStart                                  int      `json:"PreviewImageStart"`
	PreviewJXL                                         string   `json:"PreviewJXL"`
	PreviewJXLLength                                   int      `json:"PreviewJXLLength"`
	PreviewJXLStart                                    int      `json:"PreviewJXLStart"`
	PreviewSettingsDigest                              string   `json:"PreviewSettingsDigest"`
	ProcessVersion                                     float64  `json:"ProcessVersion"`
	ProfileCalibrationSig                              string   `json:"ProfileCalibrationSig"`
	ProfileCopyright                                   string   `json:"ProfileCopyright"`
	ProfileEmbedPolicy                                 int      `json:"ProfileEmbedPolicy"`
	ProfileGainTableMap                                string   `json:"ProfileGainTableMap"`
	ProfileGainTableMap2                               string   `json:"ProfileGainTableMap2"`
	ProfileGroupName                                   string   `json:"ProfileGroupName"`
	ProfileHueSatMapData1                              string   `json:"ProfileHueSatMapData1"`
	ProfileHueSatMapData2                              string   `json:"ProfileHueSatMapData2"`
	ProfileHueSatMapDims                               string   `json:"ProfileHueSatMapDims"`
	ProfileLookTableData                               string   `json:"ProfileLookTableData"`
	ProfileLookTableDims                               string   `json:"ProfileLookTableDims"`
	ProfileName                                        string   `json:"ProfileName"`
	ProfileToneCurve                                   string   `json:"ProfileToneCurve"`
	RGBTables                                          string   `json:"RGBTables"`
	RawDataUniqueID                                    string   `json:"RawDataUniqueID"`
	RawFileName                                        string   `json:"RawFileName"`
	RecommendedExposureIndex                           int      `json:"RecommendedExposureIndex"`
	RedHue                                             int      `json:"RedHue"`
	RedSaturation                                      int      `json:"RedSaturation"`
	ReferenceBlackWhite                                string   `json:"ReferenceBlackWhite"`
	RowsPerStrip                                       int      `json:"RowsPerStrip"`
	RunTimeEpoch                                       int      `json:"RunTimeEpoch"`
	RunTimeFlags                                       int      `json:"RunTimeFlags"`
	RunTimeScale                                       int      `json:"RunTimeScale"`
	RunTimeSincePowerUp                                float64  `json:"RunTimeSincePowerUp"`
	RunTimeValue                                       int64    `json:"RunTimeValue"`
	SamplesPerPixel                                    int      `json:"SamplesPerPixel"`
	Saturation                                         int      `json:"Saturation"`
	SaturationAdjustmentAqua                           int      `json:"SaturationAdjustmentAqua"`
	SaturationAdjustmentBlue                           int      `json:"SaturationAdjustmentBlue"`
	SaturationAdjustmentGreen                          int      `json:"SaturationAdjustmentGreen"`
	SaturationAdjustmentMagenta                        int      `json:"SaturationAdjustmentMagenta"`
	SaturationAdjustmentOrange                         int      `json:"SaturationAdjustmentOrange"`
	SaturationAdjustmentPurple                         int      `json:"SaturationAdjustmentPurple"`
	SaturationAdjustmentRed                            int      `json:"SaturationAdjustmentRed"`
	SaturationAdjustmentYellow                         int      `json:"SaturationAdjustmentYellow"`
	ScaleFactor35Efl                                   float64  `json:"ScaleFactor35efl"`
	SceneCaptureType                                   int      `json:"SceneCaptureType"`
	SceneType                                          int      `json:"SceneType"`
	SemanticInstanceID                                 int      `json:"SemanticInstanceID"`
	SemanticName                                       string   `json:"SemanticName"`
	SemanticSegmentationMatteVersion                   int      `json:"SemanticSegmentationMatteVersion"`
	SensingMethod                                      int      `json:"SensingMethod"`
	SensitivityType                                    int      `json:"SensitivityType"`
	SerialNumber                                       string   `json:"SerialNumber"`
	ShadowScale                                        int      `json:"ShadowScale"`
	ShadowTint                                         int      `json:"ShadowTint"`
	Shadows2012                                        int      `json:"Shadows2012"`
	SharpenDetail                                      int      `json:"SharpenDetail"`
	SharpenEdgeMasking                                 int      `json:"SharpenEdgeMasking"`
	SharpenRadius                                      string   `json:"SharpenRadius"`
	Sharpness                                          int      `json:"Sharpness"`
	ShutterSpeed                                       float64  `json:"ShutterSpeed"`
	ShutterSpeedValue                                  float64  `json:"ShutterSpeedValue"`
	SignalToNoiseRatio                                 float64  `json:"SignalToNoiseRatio"`
	Software                                           float64  `json:"Software"`
	SourceFile                                         string   `json:"SourceFile"`
	SplitToningBalance                                 int      `json:"SplitToningBalance"`
	SplitToningHighlightHue                            int      `json:"SplitToningHighlightHue"`
	SplitToningHighlightSaturation                     int      `json:"SplitToningHighlightSaturation"`
	SplitToningShadowHue                               int      `json:"SplitToningShadowHue"`
	SplitToningShadowSaturation                        int      `json:"SplitToningShadowSaturation"`
	SubSecCreateDate                                   string   `json:"SubSecCreateDate"`
	SubSecDateTimeOriginal                             string   `json:"SubSecDateTimeOriginal"`
	SubSecModifyDate                                   string   `json:"SubSecModifyDate"`
	SubSecTimeDigitized                                string   `json:"SubSecTimeDigitized"`
	SubSecTimeOriginal                                 string   `json:"SubSecTimeOriginal"`
	SubfileType                                        int      `json:"SubfileType"`
	SubjectArea                                        string   `json:"SubjectArea"`
	Texture                                            int      `json:"Texture"`
	TileByteCounts                                     string   `json:"TileByteCounts"`
	TileLength                                         int      `json:"TileLength"`
	TileOffsets                                        string   `json:"TileOffsets"`
	TileWidth                                          int      `json:"TileWidth"`
	Tint                                               string   `json:"Tint"`
	ToneCurveName2012                                  string   `json:"ToneCurveName2012"`
	ToneCurvePV2012                                    []string `json:"ToneCurvePV2012"`
	ToneCurvePV2012Blue                                []string `json:"ToneCurvePV2012Blue"`
	ToneCurvePV2012Green                               []string `json:"ToneCurvePV2012Green"`
	ToneCurvePV2012Red                                 []string `json:"ToneCurvePV2012Red"`
	Transformation                                     string   `json:"Transformation"`
	UniqueCameraModel                                  string   `json:"UniqueCameraModel"`
	Version                                            float64  `json:"Version"`
	Vibrance                                           int      `json:"Vibrance"`
	VignetteAmount                                     int      `json:"VignetteAmount"`
	VignetteCorrectionAlreadyApplied                   bool     `json:"VignetteCorrectionAlreadyApplied"`
	VirtualFocalLength                                 float64  `json:"VirtualFocalLength"`
	VirtualImageXCenter                                float64  `json:"VirtualImageXCenter"`
	VirtualImageYCenter                                float64  `json:"VirtualImageYCenter"`
	Warning                                            string   `json:"Warning"`
	WhiteBalance                                       string   `json:"WhiteBalance"`
	WhiteLevel                                         int      `json:"WhiteLevel"`
	Whites2012                                         int      `json:"Whites2012"`
	XMPToolkit                                         string   `json:"XMPToolkit"`
	YCbCrCoefficients                                  string   `json:"YCbCrCoefficients"`
	YCbCrPositioning                                   int      `json:"YCbCrPositioning"`
	YCbCrSubSampling                                   string   `json:"YCbCrSubSampling"`
}

func (Dng) MediaType() string { return MediaTypeRaw }

func (d Dng) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:      ftoa(d.ExifToolVersion),
		SourceFile:           d.SourceFile,
		Directory:            d.Directory,
		FileName:             d.FileName,
		FileSize:             itoa(d.FileSize),
		FilePermissions:      itoa(d.FilePermissions),
		FileType:             d.FileType,
		FileTypeExtension:    d.FileTypeExtension,
		MIMEType:             d.MIMEType,
		FileModifyDate:       d.FileModifyDate,
		FileAccessDate:       d.FileAccessDate,
		FileInodeChangeDate:  d.FileInodeChangeDate,
		ImageWidth:           itoa(d.ImageWidth),
		ImageHeight:          itoa(d.ImageHeight),
		ImageSize:            d.ImageSize,
		Megapixels:           ftoa(d.Megapixels),
		Orientation:          itoa(d.Orientation),
		Make:                 d.Make,
		Model:                d.Model,
		LensModel:            d.LensModel,
		Software:             ftoa(d.Software),
		CreateDate:           d.CreateDate,
		ModifyDate:           d.ModifyDate,
		DateTimeOriginal:     d.DateTimeOriginal,
		ISO:                  itoa(d.Iso),
		Aperture:             ftoa(d.Aperture),
		FNumber:              ftoa(d.FNumber),
		FocalLength:          ftoa(d.FocalLength),
		ExposureTime:         ftoa(d.ExposureTime),
		ShutterSpeed:         ftoa(d.ShutterSpeed),
		ExposureMode:         itoa(d.ExposureMode),
		ExposureProgram:      itoa(d.ExposureProgram),
		ExposureCompensation: itoa(d.ExposureCompensation),
		Flash:                itoa(d.Flash),
		MeteringMode:         itoa(d.MeteringMode),
		WhiteBalance:         d.WhiteBalance,
		GPSLatitude:          ftoa(d.GPSLatitude),
		GPSLongitude:         ftoa(d.GPSLongitude),
		GPSAltitude:          ftoa(d.GPSAltitude),
		GPSAltitudeRef:       itoa(d.GPSAltitudeRef),
		GPSPosition:          d.GPSPosition,
	}
}
