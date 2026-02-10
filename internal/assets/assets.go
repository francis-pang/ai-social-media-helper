// Package assets provides embedded static assets for the application.
package assets

import (
	_ "embed"
)

// FrancisReferencePhoto contains the reference photo of Francis for person identification.
// This is used by Gemini to identify Francis in photos during selection and analysis.
// See DDR-017: Francis Reference Photo for Person Identification.
//
//go:embed reference-photos/francis-reference.jpg
var FrancisReferencePhoto []byte

// FrancisReferenceMIMEType is the MIME type of the Francis reference photo.
const FrancisReferenceMIMEType = "image/jpeg"
