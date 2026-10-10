function main(): i32 {
  let t = 0;
  for (let i = 0; i < 5 && t < 100; i++) { t += i; }
  console.log(t);
  return 0;
}
