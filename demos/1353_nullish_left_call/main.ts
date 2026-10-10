function getv(n: i32): i32 | null {
  if (n > 0) {
    return n;
  }
  return null;
}
function main(): i32 {
  console.log(getv(0) ?? 9);
  console.log(getv(6) ?? 9);
  return 0;
}
