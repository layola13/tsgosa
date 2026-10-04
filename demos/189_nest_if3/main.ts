function sign(x: i32): i32 {
  if (x > 0) {
    if (x > 100) {
      return 2;
    }
    return 1;
  } else if (x < 0) {
    return -1;
  }
  return 0;
}
function main(): i32 {
  console.log(sign(200), sign(5), sign(-3), sign(0));
  return 0;
}