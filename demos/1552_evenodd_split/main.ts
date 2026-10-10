function main(): i32 {
  const a: i32[] = [1, 2, 3, 4, 5, 6];
  let e = 0;
  let o = 0;
  for (const v of a) { if (v % 2 == 0) { e = e + v; } else { o = o + v; } }
  console.log(e);
  console.log(o);
  return 0;
}
