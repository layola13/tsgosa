function main(): i32 {
  try {
    console.log(0);
    throw 7;
  } catch (e) {
    console.log(e);
  }
  return 0;
}
