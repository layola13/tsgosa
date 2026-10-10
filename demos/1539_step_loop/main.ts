function main(): i32 {
  let total = 0;
  for (let i = 10; i > 0; i = i - 2) { total = total + i; }
  console.log(total);
  return 0;
}
