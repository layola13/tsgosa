function isLeap(y: i32): i32 {
  if (y % 400 == 0) {
    return 1;
  }
  if (y % 100 == 0) {
    return 0;
  }
  if (y % 4 == 0) {
    return 1;
  }
  return 0;
}
function main(): i32 {
  console.log(isLeap(2000), isLeap(1900), isLeap(2024));
  return 0;
}