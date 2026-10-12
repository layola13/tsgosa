function eq(a: boolean, b: boolean): number {
  if (a == b) { return 1; }
  return 0;
}
function main(): i32 {
  console.log(eq(true, false));
  console.log(eq(true, true));
  return 0;
}
