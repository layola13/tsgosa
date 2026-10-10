function main(): i32 {
  let x: i32 | null = 5;
  console.log(x ?? 0);
  x = null;
  console.log(x ?? 7);
  return 0;
}
