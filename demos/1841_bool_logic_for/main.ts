function main(): i32 {
  let n = 0;
  const f = true;
  const g = false;
  for (let i = 0; f && i < 2; i = i + 1) {
    n = n + 10;
  }
  console.log(n);
  return 0;
}
