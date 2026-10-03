function max(a: i32, b: i32): i32 {
  if (a > b) {
    return a;
  } else {
    return b;
  }
}
function main(): i32 {
  console.log(max(7, 42));
  console.log(max(-3, -9));
  return 0;
}
