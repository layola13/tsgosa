function main(): i32 {
  const f = true;
  let n = 1;
  do {
    n = n + 1;
  } while (f && n < 3);
  console.log(n);
  return 0;
}
