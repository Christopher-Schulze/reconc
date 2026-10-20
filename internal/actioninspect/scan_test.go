package actioninspect

func loadSharedBuiltinPack() (compiledDetectorPack, error) {
	programs, err := loadBuiltinDetectorPrograms()
	if err != nil {
		return compiledDetectorPack{}, err
	}
	return programs.pack, nil
}
