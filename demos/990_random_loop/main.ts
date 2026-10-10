function main(): i32 {
  let t = 0;
  for (let i = 0; i < 3; i++) {
    if (Math.random() >= 0) { t += 1; }
  }
  console.log(t);
  return 0;
}
