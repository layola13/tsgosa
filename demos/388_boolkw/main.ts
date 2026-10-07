function isYes(x: bool): bool {
  return x;
}
function main(): i32 {
  let b: bool = true;
  console.log(isYes(b));
  let bs: boolean[] = [true, false];
  console.log(bs.length);
  console.log(bs[0]);
  return 0;
}
