function abs2(n: i32): i32 {
  if (n < 0) {
    return 0 - n;
  }
  return n;
}
function main(): i32 {
  console.log(abs2(0 - 5));
  console.log(abs2(3));
  return 0;
}
