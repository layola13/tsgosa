function main(): i32 {
  let x = 0;
  for (let i = 0; i < 5; i = i + 1) { x = x + 2 * i; }
  console.log(x);
  return 0;
}
