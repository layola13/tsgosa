function main(): i32 {
  let t = 0;
  for (let i = 0, j = 100; i < 3; i++) { t += i + j; }
  console.log(t);
  return 0;
}
