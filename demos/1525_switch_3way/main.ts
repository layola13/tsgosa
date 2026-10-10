function main(): i32 {
  const x: i32 = 2;
  let r = 0;
  switch (x) { case 1: r = 10; break; case 2: r = 20; break; default: r = 30; }
  console.log(r);
  return 0;
}
