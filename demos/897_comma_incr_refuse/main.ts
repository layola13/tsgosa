function main(): i32 {
  let t = 0;
  for (let i = 0, j = 10; i < 5; i++, j--) { t += i + j; }
  console.log(t);
  return 0;
}
