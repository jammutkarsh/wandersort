package classifier

type Mp4 struct {
	AltTimecodeTimeFormat                           string   `json:"AltTimecodeTimeFormat"`
	AltTimecodeTimeValue                            string   `json:"AltTimecodeTimeValue"`
	AndroidCaptureFPS                               int      `json:"AndroidCaptureFPS"`
	AndroidMake                                     string   `json:"AndroidMake"`
	AndroidModel                                    string   `json:"AndroidModel"`
	AndroidTimeZone                                 string   `json:"AndroidTimeZone"`
	AndroidVersion                                  int      `json:"AndroidVersion"`
	AndroidVideoTemporalLayersCount                 string   `json:"AndroidVideoTemporalLayersCount"`
	AppleMakerNote74                                int      `json:"Apple-maker-note74"`
	AppleMakerNote97                                int      `json:"Apple-maker-note97"`
	AudioBitsPerSample                              int      `json:"AudioBitsPerSample"`
	AudioChannelType                                string   `json:"AudioChannelType"`
	AudioChannels                                   int      `json:"AudioChannels"`
	AudioFormat                                     string   `json:"AudioFormat"`
	AudioSampleRate                                 int      `json:"AudioSampleRate"`
	AudioSampleType                                 string   `json:"AudioSampleType"`
	Author                                          string   `json:"Author"`
	AverageBitrate                                  int      `json:"AverageBitrate"`
	AvgBitrate                                      int      `json:"AvgBitrate"`
	Balance                                         int      `json:"Balance"`
	BitDepth                                        int      `json:"BitDepth"`
	BufferSize                                      int      `json:"BufferSize"`
	CameraLensIrisfnumber                           string   `json:"CameraLensIrisfnumber"`
	CameraLensIrisfnumberEngUS                      string   `json:"CameraLensIrisfnumber-eng-US"`
	CleanApertureDimensions                         string   `json:"CleanApertureDimensions"`
	CleanApertureHeight                             int      `json:"CleanApertureHeight"`
	CleanApertureOffsetX                            int      `json:"CleanApertureOffsetX"`
	CleanApertureOffsetY                            int      `json:"CleanApertureOffsetY"`
	CleanApertureWidth                              int      `json:"CleanApertureWidth"`
	ColorPrimaries                                  int      `json:"ColorPrimaries"`
	ColorProfiles                                   string   `json:"ColorProfiles"`
	Comment                                         string   `json:"Comment"`
	CompatibleBrands                                []string `json:"CompatibleBrands"`
	CompressorID                                    string   `json:"CompressorID"`
	CompressorName                                  string   `json:"CompressorName"`
	ContentDescribes                                int      `json:"ContentDescribes"`
	ContentIdentifier                               string   `json:"ContentIdentifier"`
	CreateDate                                      string   `json:"CreateDate"`
	CreationDate                                    string   `json:"CreationDate"`
	CreatorTool                                     string   `json:"CreatorTool"`
	CurrentTime                                     int      `json:"CurrentTime"`
	DerivedFromDocumentID                           string   `json:"DerivedFromDocumentID"`
	DerivedFromInstanceID                           string   `json:"DerivedFromInstanceID"`
	DerivedFromOriginalDocumentID                   string   `json:"DerivedFromOriginalDocumentID"`
	Description                                     string   `json:"Description"`
	Directory                                       string   `json:"Directory"`
	DocumentID                                      string   `json:"DocumentID"`
	Duration                                        float64  `json:"Duration"`
	DurationScale                                   float64  `json:"DurationScale"`
	DurationValue                                   int      `json:"DurationValue"`
	EncodedPixelsDimensions                         string   `json:"EncodedPixelsDimensions"`
	Encoder                                         string   `json:"Encoder"`
	ExifToolVersion                                 float64  `json:"ExifToolVersion"`
	FileAccessDate                                  string   `json:"FileAccessDate"`
	FileInodeChangeDate                             string   `json:"FileInodeChangeDate"`
	FileModifyDate                                  string   `json:"FileModifyDate"`
	FileName                                        string   `json:"FileName"`
	FilePermissions                                 int      `json:"FilePermissions"`
	FileSize                                        int      `json:"FileSize"`
	FileType                                        string   `json:"FileType"`
	FileTypeExtension                               string   `json:"FileTypeExtension"`
	FocalLengthIn35MmFormat                         int      `json:"FocalLengthIn35mmFormat"`
	FocalLengthIn35MmFormatEngUS                    int      `json:"FocalLengthIn35mmFormat-eng-US"`
	Format                                          string   `json:"Format"`
	FullFrameRatePlaybackIntent                     int      `json:"FullFrameRatePlaybackIntent"`
	GPSAltitude                                     float64  `json:"GPSAltitude"`
	GPSAltitudeRef                                  int      `json:"GPSAltitudeRef"`
	GPSCoordinates                                  string   `json:"GPSCoordinates"`
	GPSCoordinatesEngIN                             string   `json:"GPSCoordinates-eng-IN"`
	GPSLatitude                                     float64  `json:"GPSLatitude"`
	GPSLongitude                                    float64  `json:"GPSLongitude"`
	GPSPosition                                     string   `json:"GPSPosition"`
	GenBalance                                      int      `json:"GenBalance"`
	GenFlags                                        string   `json:"GenFlags"`
	GenGraphicsMode                                 int      `json:"GenGraphicsMode"`
	GenMediaVersion                                 int      `json:"GenMediaVersion"`
	GenOpColor                                      string   `json:"GenOpColor"`
	GoogleStartTime                                 int      `json:"GoogleStartTime"`
	GoogleTrackDuration                             float64  `json:"GoogleTrackDuration"`
	GraphicsMode                                    int      `json:"GraphicsMode"`
	HandlerClass                                    string   `json:"HandlerClass"`
	HandlerDescription                              string   `json:"HandlerDescription"`
	HandlerType                                     string   `json:"HandlerType"`
	HandlerVendorID                                 string   `json:"HandlerVendorID"`
	HistoryAction                                   []string `json:"HistoryAction"`
	HistoryChanged                                  []string `json:"HistoryChanged"`
	HistoryInstanceID                               []string `json:"HistoryInstanceID"`
	HistorySoftwareAgent                            []string `json:"HistorySoftwareAgent"`
	HistoryWhen                                     []string `json:"HistoryWhen"`
	ImageHeight                                     int      `json:"ImageHeight"`
	ImageSize                                       string   `json:"ImageSize"`
	ImageWidth                                      int      `json:"ImageWidth"`
	IngredientsDocumentID                           []string `json:"IngredientsDocumentID"`
	IngredientsFilePath                             []string `json:"IngredientsFilePath"`
	IngredientsFromPart                             []string `json:"IngredientsFromPart"`
	IngredientsInstanceID                           []string `json:"IngredientsInstanceID"`
	IngredientsMaskMarkers                          []string `json:"IngredientsMaskMarkers"`
	IngredientsToPart                               []string `json:"IngredientsToPart"`
	InstanceID                                      string   `json:"InstanceID"`
	LensID                                          string   `json:"LensID"`
	LensModel                                       string   `json:"LensModel"`
	LensModelEngUS                                  string   `json:"LensModel-eng-US"`
	LivePhotoSubjectRelightingAppliedCurveParameter float64  `json:"Live-photoSubject-relighting-applied-curve-parameter"`
	LivePhotoAuto                                   int      `json:"LivePhotoAuto"`
	LivePhotoVitalityScore                          int      `json:"LivePhotoVitalityScore"`
	LivePhotoVitalityScoringVersion                 int      `json:"LivePhotoVitalityScoringVersion"`
	LocationAccuracyHorizontal                      float64  `json:"LocationAccuracyHorizontal"`
	MIMEType                                        string   `json:"MIMEType"`
	MacAtomApplicationCode                          int      `json:"MacAtomApplicationCode"`
	MacAtomInvocationAppleEvent                     int      `json:"MacAtomInvocationAppleEvent"`
	MajorBrand                                      string   `json:"MajorBrand"`
	Make                                            string   `json:"Make"`
	MatrixCoefficients                              int      `json:"MatrixCoefficients"`
	MatrixStructure                                 string   `json:"MatrixStructure"`
	MaxBitrate                                      int      `json:"MaxBitrate"`
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
	MetadataDate                                    string   `json:"MetadataDate"`
	MinorVersion                                    string   `json:"MinorVersion"`
	Model                                           string   `json:"Model"`
	ModifyDate                                      string   `json:"ModifyDate"`
	MovieHeaderVersion                              int      `json:"MovieHeaderVersion"`
	NextTrackID                                     int      `json:"NextTrackID"`
	OpColor                                         string   `json:"OpColor"`
	Orientation                                     int      `json:"Orientation"`
	OriginalDocumentID                              string   `json:"OriginalDocumentID"`
	OriginalPathHashKey                             string   `json:"OriginalPathHashKey"`
	OtherFormat                                     string   `json:"OtherFormat"`
	PantryAeProjectLinkCompositionID                int      `json:"PantryAeProjectLinkCompositionID"`
	PantryAeProjectLinkFullPath                     string   `json:"PantryAeProjectLinkFullPath"`
	PantryAeProjectLinkRenderOutputModuleIndex      int      `json:"PantryAeProjectLinkRenderOutputModuleIndex"`
	PantryAeProjectLinkRenderQueueItemID            int      `json:"PantryAeProjectLinkRenderQueueItemID"`
	PantryAltTimecodeTimeFormat                     string   `json:"PantryAltTimecodeTimeFormat"`
	PantryAltTimecodeTimeValue                      string   `json:"PantryAltTimecodeTimeValue"`
	PantryArtist                                    string   `json:"PantryArtist"`
	PantryAudioChannelType                          string   `json:"PantryAudioChannelType"`
	PantryAudioSampleRate                           int      `json:"PantryAudioSampleRate"`
	PantryAudioSampleType                           string   `json:"PantryAudioSampleType"`
	PantryBitsPerSample                             []int    `json:"PantryBitsPerSample"`
	PantryColorMode                                 int      `json:"PantryColorMode"`
	PantryColorSpace                                int      `json:"PantryColorSpace"`
	PantryCreateDate                                string   `json:"PantryCreateDate"`
	PantryCreatorTool                               string   `json:"PantryCreatorTool"`
	PantryDerivedFromDocumentID                     string   `json:"PantryDerivedFromDocumentID"`
	PantryDerivedFromInstanceID                     string   `json:"PantryDerivedFromInstanceID"`
	PantryDerivedFromOriginalDocumentID             string   `json:"PantryDerivedFromOriginalDocumentID"`
	PantryDocumentAncestors                         []string `json:"PantryDocumentAncestors"`
	PantryDocumentID                                string   `json:"PantryDocumentID"`
	PantryDurationScale                             float64  `json:"PantryDurationScale"`
	PantryDurationValue                             int      `json:"PantryDurationValue"`
	PantryExifImageHeight                           int      `json:"PantryExifImageHeight"`
	PantryExifImageWidth                            int      `json:"PantryExifImageWidth"`
	PantryExifVersion                               string   `json:"PantryExifVersion"`
	PantryFormat                                    string   `json:"PantryFormat"`
	PantryHistoryAction                             string   `json:"PantryHistoryAction"`
	PantryHistoryChanged                            string   `json:"PantryHistoryChanged"`
	PantryHistoryInstanceID                         string   `json:"PantryHistoryInstanceID"`
	PantryHistoryParameters                         string   `json:"PantryHistoryParameters"`
	PantryHistorySoftwareAgent                      string   `json:"PantryHistorySoftwareAgent"`
	PantryHistoryWhen                               string   `json:"PantryHistoryWhen"`
	PantryICCProfileName                            string   `json:"PantryICCProfileName"`
	PantryImageHeight                               int      `json:"PantryImageHeight"`
	PantryImageWidth                                int      `json:"PantryImageWidth"`
	PantryIngredientsDocumentID                     string   `json:"PantryIngredientsDocumentID"`
	PantryIngredientsFilePath                       string   `json:"PantryIngredientsFilePath"`
	PantryIngredientsFromPart                       string   `json:"PantryIngredientsFromPart"`
	PantryIngredientsInstanceID                     string   `json:"PantryIngredientsInstanceID"`
	PantryIngredientsMaskMarkers                    string   `json:"PantryIngredientsMaskMarkers"`
	PantryIngredientsToPart                         string   `json:"PantryIngredientsToPart"`
	PantryInstanceID                                string   `json:"PantryInstanceID"`
	PantryLegacyIPTCDigest                          string   `json:"PantryLegacyIPTCDigest"`
	PantryMacAtomApplicationCode                    int      `json:"PantryMacAtomApplicationCode"`
	PantryMacAtomInvocationAppleEvent               int      `json:"PantryMacAtomInvocationAppleEvent"`
	PantryMake                                      string   `json:"PantryMake"`
	PantryMetadataDate                              string   `json:"PantryMetadataDate"`
	PantryModifyDate                                string   `json:"PantryModifyDate"`
	PantryOrientation                               int      `json:"PantryOrientation"`
	PantryOriginalDocumentID                        string   `json:"PantryOriginalDocumentID"`
	PantryPhotometricInterpretation                 int      `json:"PantryPhotometricInterpretation"`
	PantryProjectRefType                            string   `json:"PantryProjectRefType"`
	PantryResolutionUnit                            int      `json:"PantryResolutionUnit"`
	PantrySamplesPerPixel                           int      `json:"PantrySamplesPerPixel"`
	PantryShotDate                                  string   `json:"PantryShotDate"`
	PantryStartTimeSampleSize                       int      `json:"PantryStartTimeSampleSize"`
	PantryStartTimeScale                            int      `json:"PantryStartTimeScale"`
	PantryStartTimecodeTimeFormat                   string   `json:"PantryStartTimecodeTimeFormat"`
	PantryStartTimecodeTimeValue                    string   `json:"PantryStartTimecodeTimeValue"`
	PantryTextLayerName                             string   `json:"PantryTextLayerName"`
	PantryTextLayerText                             string   `json:"PantryTextLayerText"`
	PantryTitle                                     string   `json:"PantryTitle"`
	PantryTracksFrameRate                           []string `json:"PantryTracksFrameRate"`
	PantryTracksMarkersCuePointParamsKey            string   `json:"PantryTracksMarkersCuePointParamsKey"`
	PantryTracksMarkersCuePointParamsValue          string   `json:"PantryTracksMarkersCuePointParamsValue"`
	PantryTracksMarkersGUID                         string   `json:"PantryTracksMarkersGuid"`
	PantryTracksMarkersStartTime                    int      `json:"PantryTracksMarkersStartTime"`
	PantryTracksMarkersType                         string   `json:"PantryTracksMarkersType"`
	PantryTracksTrackName                           []string `json:"PantryTracksTrackName"`
	PantryVideoFieldOrder                           string   `json:"PantryVideoFieldOrder"`
	PantryVideoFrameRate                            int      `json:"PantryVideoFrameRate"`
	PantryVideoFrameSizeH                           int      `json:"PantryVideoFrameSizeH"`
	PantryVideoFrameSizeUnit                        string   `json:"PantryVideoFrameSizeUnit"`
	PantryVideoFrameSizeW                           int      `json:"PantryVideoFrameSizeW"`
	PantryVideoPixelAspectRatio                     int      `json:"PantryVideoPixelAspectRatio"`
	PantryWindowsAtomExtension                      string   `json:"PantryWindowsAtomExtension"`
	PantryWindowsAtomInvocationFlags                string   `json:"PantryWindowsAtomInvocationFlags"`
	PantryWindowsAtomUncProjectPath                 string   `json:"PantryWindowsAtomUncProjectPath"`
	PantryXResolution                               int      `json:"PantryXResolution"`
	PantryYResolution                               int      `json:"PantryYResolution"`
	PixelAspectRatio                                string   `json:"PixelAspectRatio"`
	PlayMode                                        string   `json:"PlayMode"`
	PosterTime                                      int      `json:"PosterTime"`
	PreferredRate                                   int      `json:"PreferredRate"`
	PreferredVolume                                 int      `json:"PreferredVolume"`
	PreviewDuration                                 int      `json:"PreviewDuration"`
	PreviewImage                                    string   `json:"PreviewImage"`
	PreviewTime                                     int      `json:"PreviewTime"`
	ProductionApertureDimensions                    string   `json:"ProductionApertureDimensions"`
	ProjectRefType                                  string   `json:"ProjectRefType"`
	PurchaseFileFormat                              string   `json:"PurchaseFileFormat"`
	Rotation                                        int      `json:"Rotation"`
	SamsungModel                                    string   `json:"SamsungModel"`
	SelectionDuration                               int      `json:"SelectionDuration"`
	SelectionTime                                   int      `json:"SelectionTime"`
	SmartstyleBypassed                              int      `json:"SmartstyleBypassed"`
	SmartstyleCast                                  int      `json:"SmartstyleCast"`
	SmartstyleColor                                 int      `json:"SmartstyleColor"`
	SmartstyleIntensity                             int      `json:"SmartstyleIntensity"`
	SmartstyleRenderingVersion                      int      `json:"SmartstyleRendering-version"`
	SmartstyleTone                                  int      `json:"SmartstyleTone"`
	Software                                        float64  `json:"Software"`
	SourceFile                                      string   `json:"SourceFile"`
	SourceImageHeight                               int      `json:"SourceImageHeight"`
	SourceImageWidth                                int      `json:"SourceImageWidth"`
	StartTimeSampleSize                             int      `json:"StartTimeSampleSize"`
	StartTimeScale                                  int      `json:"StartTimeScale"`
	StartTimecode                                   string   `json:"StartTimecode"`
	StartTimecodeTimeFormat                         string   `json:"StartTimecodeTimeFormat"`
	StartTimecodeTimeValue                          string   `json:"StartTimecodeTimeValue"`
	TimeScale                                       int      `json:"TimeScale"`
	TrackCreateDate                                 string   `json:"TrackCreateDate"`
	TrackDuration                                   float64  `json:"TrackDuration"`
	TrackHeaderVersion                              int      `json:"TrackHeaderVersion"`
	TrackID                                         int      `json:"TrackID"`
	TrackLayer                                      int      `json:"TrackLayer"`
	TrackModifyDate                                 string   `json:"TrackModifyDate"`
	TrackVolume                                     int      `json:"TrackVolume"`
	TransferCharacteristics                         int      `json:"TransferCharacteristics"`
	UserDataEng                                     string   `json:"UserData_eng"`
	UserDataEngYkn                                  string   `json:"UserData_eng-ykn"`
	VideoFieldOrder                                 string   `json:"VideoFieldOrder"`
	VideoFrameRate                                  float64  `json:"VideoFrameRate"`
	VideoFrameSizeH                                 int      `json:"VideoFrameSizeH"`
	VideoFrameSizeUnit                              string   `json:"VideoFrameSizeUnit"`
	VideoFrameSizeW                                 int      `json:"VideoFrameSizeW"`
	VideoFullRangeFlag                              int      `json:"VideoFullRangeFlag"`
	VideoPixelAspectRatio                           int      `json:"VideoPixelAspectRatio"`
	Warning                                         string   `json:"Warning"`
	WindowsAtomExtension                            string   `json:"WindowsAtomExtension"`
	WindowsAtomInvocationFlags                      string   `json:"WindowsAtomInvocationFlags"`
	WindowsAtomUncProjectPath                       string   `json:"WindowsAtomUncProjectPath"`
	XMPToolkit                                      string   `json:"XMPToolkit"`
	XResolution                                     int      `json:"XResolution"`
	YResolution                                     int      `json:"YResolution"`
}

func (Mp4) MediaType() string { return MediaTypeVideo }

func (m Mp4) ToCommon() CommonMetadata {
	return CommonMetadata{
		ExifToolVersion:     ftoa(m.ExifToolVersion),
		SourceFile:          m.SourceFile,
		Directory:           m.Directory,
		FileName:            m.FileName,
		FileSize:            itoa(m.FileSize),
		FilePermissions:     itoa(m.FilePermissions),
		FileType:            m.FileType,
		FileTypeExtension:   m.FileTypeExtension,
		MIMEType:            m.MIMEType,
		FileModifyDate:      m.FileModifyDate,
		FileAccessDate:      m.FileAccessDate,
		FileInodeChangeDate: m.FileInodeChangeDate,
		ImageWidth:          itoa(m.ImageWidth),
		ImageHeight:         itoa(m.ImageHeight),
		ImageSize:           m.ImageSize,
		Megapixels:          ftoa(m.Megapixels),
		Orientation:         itoa(m.Orientation),
		Make:                m.Make,
		Model:               m.Model,
		LensModel:           m.LensModel,
		Software:            ftoa(m.Software),
		CreateDate:          m.CreateDate,
		ModifyDate:          m.ModifyDate,
		GPSLatitude:         ftoa(m.GPSLatitude),
		GPSLongitude:        ftoa(m.GPSLongitude),
		GPSAltitude:         ftoa(m.GPSAltitude),
		GPSAltitudeRef:      itoa(m.GPSAltitudeRef),
		GPSPosition:         m.GPSPosition,
	}
}
