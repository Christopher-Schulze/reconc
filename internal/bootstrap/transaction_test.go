package bootstrap

func captureCreatedDirectory(path string) (createdDirectory, error) {
	identity, err := captureDirectoryIdentity(path)
	if err != nil {
		return createdDirectory{}, err
	}
	return createdDirectory{path: path, identity: identity}, nil
}
