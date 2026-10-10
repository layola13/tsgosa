function pick(n: i32): string {
  return n > 0 ? "pos" : "neg";
}
function main(): i32 {
  console.log(pick(5));
  console.log(pick(-2));
  return 0;
}
