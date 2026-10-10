function main(): i32 {
  let t = 0;
  for (const [i, v] of [10, 20].entries()) { t += i + v; }
  console.log(t);
  return 0;
}
