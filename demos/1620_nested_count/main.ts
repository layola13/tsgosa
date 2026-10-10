function main(): i32 {
  let t = 0;
  for (let i = 0; i < 3; i = i + 1) { for (let j = 0; j < 2; j = j + 1) { t = t + 1; } }
  console.log(t);
  return 0;
}
