function neg(b: boolean): i32 { return b ? 0 : 1; }
function main(): i32 {
  console.log(neg(true));
  console.log(neg(false));
  return 0;
}
