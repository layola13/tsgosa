function isNull(x: i32 | null): i32 {
  if (x == null) {
    return 1;
  }
  return 0;
}
function main(): i32 {
  let x: null = null;
  console.log(isNull(x));
  console.log(isNull(5));
  return 0;
}
