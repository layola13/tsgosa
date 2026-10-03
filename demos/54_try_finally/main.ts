function main(): i32 {
  let t: i32 = 0;
  try {
    t = 1;
  } finally {
    t = t + 10;
  }
  console.log(t);
  return 0;
}
