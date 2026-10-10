function main(): i32 {
  let t = 0;
  for (let i = 0; i < 3; i++) {
    try {
      t += i;
    } finally {
      t += 100;
    }
  }
  console.log(t);
  return 0;
}
