package mcp

// winAttrHidden is FILE_ATTRIBUTE_HIDDEN (0x2). It's defined here rather than
// via syscall so the bit logic compiles, and can be tested, on every OS.
const winAttrHidden = 0x2

func attrsHidden(attrs uint32) bool { return attrs&winAttrHidden != 0 }
