function main(): i32 {
  let x: i32 | null = null;
  x ??= 7;
  console.log(x);
  let y: i32 | null = 3;
  y ??= 9;
  console.log(y);
  return 0;
}
