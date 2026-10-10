function main(): i32 {
  try {
    throw 5;
  } catch (e) {
    console.log(e + 1);
  }
  return 0;
}
