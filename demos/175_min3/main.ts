function min3(a: i32, b: i32, c: i32): i32 {
  let m: i32 = a;
  if (b < m) {
    m = b;
  }
  if (c < m) {
    m = c;
  }
  return m;
}
function main(): i32 {
  console.log(min3(3, 1, 2));
  return 0;
}