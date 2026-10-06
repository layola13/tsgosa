function main(): i32 {
  console.log("  ab  ".trim().length);
  console.log("  ab  ".trimStart().length);
  console.log("  ab  ".trimEnd().length);
  console.log("   ".trim().length);
  console.log("".trim().length);
  console.log("ab".trim().length);
  return 0;
}
