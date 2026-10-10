function main(): i32 {
  const a: i32[] = [1, 2, 3, 4, 5];
  let t = 0;
  for (const v of a) {
    if (v === 3) { break; }
    t += v;
  }
  console.log(t);
  return 0;
}
