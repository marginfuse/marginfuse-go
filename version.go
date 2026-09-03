package marginfuse

// Version is the released version of this module, as sent in the user-agent.
//
// A Go module has no manifest to read this from: the version is the git tag,
// and the module proxy caches a tag permanently the first time anyone fetches
// it, so a wrong one cannot be corrected in place. The release workflow
// therefore refuses to publish a tag that disagrees with this constant.
//
// The Node SDK shipped two releases reporting 0.1.0 because it had a literal
// like this one with nothing checking it.
const Version = "0.2.0"
