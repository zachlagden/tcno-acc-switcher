//go:build !production

package updatecheck

func updateAPIURL(string) string {
	return LatestReleaseAPIURL
}
