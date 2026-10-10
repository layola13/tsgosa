function main(): i32 {
  let a = 0;
  a ||= 5;
  let b = 7;
  b &&= 0;
  let c: i32|null = null;
  c ??= 9;
  console.log(a);
  console.log(b);
  console.log(c ?? -1);
  return 0;
}
