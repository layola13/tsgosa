function main(): i32 {
  let t = 100;
  for (let i = 0; i < 3; i = i + 1) { t = t - i * 10; }
  console.log(t);
  return 0;
}
