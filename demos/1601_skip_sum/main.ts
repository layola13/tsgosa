function main(): i32 {
  let s = 0;
  for (let i = 1; i <= 5; i = i + 1) { if (i == 3) { continue; } s = s + i * 10; }
  console.log(s);
  return 0;
}
