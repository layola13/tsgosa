function main(): i32 {
  let x: i32 | null = null;
  x ??= 5;
  console.log(x);
  return 0;
}
