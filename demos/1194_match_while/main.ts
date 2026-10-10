function main(): i32 {
  let t = 0;
  const m = "a1b2".match(/[0-9]/g);
  let i = 0;
  while (i < m.length) {
    t += 1;
    i += 1;
  }
  console.log(t);
  return 0;
}
