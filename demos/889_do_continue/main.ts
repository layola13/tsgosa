function main(): i32 {
  let i = 0;
  let t = 0;
  do {
    i += 1;
    if (i % 2 === 0) { continue; }
    t += i;
  } while (i < 6);
  console.log(t);
  return 0;
}
