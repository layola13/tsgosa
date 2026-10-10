function main(): i32 {
  let t = 0;
  blk: {
    t += 1;
    if (t > 0) { break blk; }
    t += 100;
  }
  console.log(t);
  return 0;
}
