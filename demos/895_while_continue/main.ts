function main(): i32 {
  let i = 0;
  let t = 0;
  while (i < 10) {
    i += 1;
    if (i % 3 === 0) { continue; }
    t += i;
  }
  console.log(t);
  return 0;
}
