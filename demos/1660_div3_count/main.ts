function main(): i32 {
  let t = 0;
  for (let i = 1; i <= 10; i = i + 1) { if (i % 3 == 0) { t = t + 1; } }
  console.log(t);
  return 0;
}
