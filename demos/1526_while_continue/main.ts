function main(): i32 {
  let t = 0;
  let i = 0;
  while (i < 6) { i = i + 1; if (i % 2 == 0) { continue; } t = t + i; }
  console.log(t);
  return 0;
}
